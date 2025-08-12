package rpclient

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

type RustPlusConfig struct {
	Server      string `json:"server"`
	Port        int    `json:"port"`
	PlayerID    uint64 `json:"player_id"`
	PlayerToken int32  `json:"player_token"`
	UseProxy    bool   `json:"use_proxy"`
}

type RustPlus struct {
	server      string
	port        int
	playerID    uint64
	playerToken int32
	useProxy    bool

	conn   *websocket.Conn
	seq    uint32
	mu     sync.Mutex
	cbs    map[uint32]func(*AppMessage) bool
	closed atomic.Bool

	// добавлено:
	wmu          sync.Mutex    // сериализует запись в websocket
	pingStop     chan struct{} // стоп-канал для ping-горутин
	lastActivity atomic.Int64  // unix nanos последнего успешного приёма сообщения

	// "События" (аналог EventEmitter)
	OnConnecting   func()
	OnConnected    func()
	OnMessage      func(*AppMessage)
	OnDisconnected func()
	OnError        func(error)
	OnRequest      func(*AppRequest)
}

func New(server string, port int, playerID uint64, playerToken int32, useProxy bool) *RustPlus {
	return &RustPlus{
		server:      server,
		port:        port,
		playerID:    playerID,
		playerToken: playerToken,
		useProxy:    useProxy,
		cbs:         make(map[uint32]func(*AppMessage) bool),
	}
}

// Connect — устанавливает WebSocket и запускает readLoop.
// Контекст можно отменить для мягкого выхода из readLoop.
func (rp *RustPlus) Connect(ctx context.Context) error {
	if rp.OnConnecting != nil {
		rp.OnConnecting()
	}
	conn, err := rp.dialAndSetup()
	if err != nil {
		return err
	}
	rp.conn = conn
	rp.closed.Store(false)

	if rp.OnConnected != nil {
		rp.OnConnected()
	}

	go rp.readLoop(ctx)
	return nil
}

func (rp *RustPlus) Disconnect() {
	rp.closed.Store(true)
	rp.closeConn()
	if rp.OnDisconnected != nil {
		rp.OnDisconnected()
	}
}

func (rp *RustPlus) IsConnected() bool {
	return rp.conn != nil && !rp.closed.Load()
}

// ========================= low-level =========================

func (rp *RustPlus) nextSeq() uint32 {
	return atomic.AddUint32(&rp.seq, 1)
}

func (rp *RustPlus) readLoop(ctx context.Context) {
	defer func() {
		rp.closed.Store(true)
		rp.closeConn()
		if rp.OnDisconnected != nil {
			rp.OnDisconnected()
		}
	}()

	// закрыть по отмене контекста
	go func() {
		<-ctx.Done()
		rp.closeConn()
	}()

	backoff := time.Second

	for {
		// подстраховка от nil (необязательно, но безопасно)
		if rp.conn == nil {
			// имитируем ошибку сети, чтобы провалиться в ветку реконнекта
			err := fmt.Errorf("connection is nil")
			if rp.OnError != nil && !rp.closed.Load() {
				rp.OnError(err)
			}
			// пойдём в реконнект ниже
		} else {
			_, data, err := rp.conn.ReadMessage()
			if err == nil {
				var msg AppMessage
				if uerr := proto.Unmarshal(data, &msg); uerr != nil {
					if rp.OnError != nil {
						rp.OnError(uerr)
					}
					continue
				}

				// успешное чтение
				rp.touchActivity()

				// callbacks по seq
				if resp := msg.GetResponse(); resp != nil && resp.Seq != nil {
					seq := *resp.Seq
					rp.mu.Lock()
					cb, ok := rp.cbs[seq]
					if ok {
						delete(rp.cbs, seq)
					}
					rp.mu.Unlock()
					if ok && cb(&msg) {
						continue
					}
				}

				if rp.OnMessage != nil {
					rp.OnMessage(&msg)
				}

				backoff = time.Second
				continue
			}

			// ошибка чтения
			if rp.OnError != nil && !rp.closed.Load() {
				rp.OnError(err)
			}
			if rp.closed.Load() {
				return
			}
		}

		// закрываем и фейлим ожидающие
		rp.closeConn()
		rp.failPendingCallbacks(fmt.Errorf("connection lost"))

		// реконнект с backoff
		for !rp.closed.Load() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				conn, derr := rp.dialAndSetup()
				if derr != nil {
					if rp.OnError != nil {
						rp.OnError(fmt.Errorf("reconnect failed (wait %v): %w", backoff, derr))
					}
					if backoff < 30*time.Second {
						backoff *= 2
						if backoff > 30*time.Second {
							backoff = 30 * time.Second
						}
					}
					continue
				}
				rp.conn = conn
				rp.touchActivity() // <<< отметим «живость» сразу после удачного коннекта
				if rp.OnConnected != nil {
					rp.OnConnected()
				}
				backoff = time.Second
				goto CONTINUE_READ
			}
		}
	CONTINUE_READ:
		continue
	}
}

// SendRequest — отправляет AppRequest, привязывая seq/игрока.
// Если cb != nil, будет вызван по ответу с тем же seq; возвращаемое cb(bool)
// если вернёт true — событие OnMessage НЕ вызовется для этого сообщения (как в JS).
func (rp *RustPlus) SendRequest(req *AppRequest, cb func(*AppMessage) bool) error {
	if rp.conn == nil {
		return fmt.Errorf("not connected")
	}
	seq := rp.nextSeq()
	req.Seq = &seq
	req.PlayerId = &rp.playerID
	req.PlayerToken = &rp.playerToken

	if cb != nil {
		rp.mu.Lock()
		rp.cbs[seq] = cb
		rp.mu.Unlock()
	}

	data, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	if rp.OnRequest != nil {
		rp.OnRequest(req)
	}

	// запись строго через один мьютекс + write‑deadline
	rp.wmu.Lock()
	_ = rp.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	werr := rp.conn.WriteMessage(websocket.BinaryMessage, data)
	rp.wmu.Unlock()

	if werr != nil {
		// сеть упала между подготовкой и записью — подчищаем cb
		rp.mu.Lock()
		delete(rp.cbs, seq)
		rp.mu.Unlock()
		return werr
	}
	return nil
}

// SendRequestAsync — как промис в JS-версии.
func (rp *RustPlus) SendRequestAsync(req *AppRequest, timeout time.Duration) (*AppResponse, error) {
	respCh := make(chan *AppResponse, 1)
	errCh := make(chan error, 1)

	err := rp.SendRequest(req, func(m *AppMessage) bool {
		if r := m.GetResponse(); r != nil {
			if r.Error != nil {
				errCh <- errors.New(r.Error.GetError())
				return true
			}
			respCh <- r
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}

	select {
	case r := <-respCh:
		return r, nil
	case e := <-errCh:
		return nil, e
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

// ========================= high-level API (полные аналоги) =========================

func (rp *RustPlus) SetEntityValue(entityID uint32, value bool, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		EntityId: proto.Uint32(entityID),
		SetEntityValue: &AppSetEntityValue{
			Value: proto.Bool(value),
		},
	}, cb)
}

func (rp *RustPlus) TurnSmartSwitchOn(entityID uint32, cb func(*AppMessage) bool) error {
	return rp.SetEntityValue(entityID, true, cb)
}

func (rp *RustPlus) TurnSmartSwitchOff(entityID uint32, cb func(*AppMessage) bool) error {
	return rp.SetEntityValue(entityID, false, cb)
}

// Strobe — как в JS: быстрое мигание (осторожно с rate limit).
func (rp *RustPlus) Strobe(ctx context.Context, entityID uint32, interval time.Duration, start bool) {
	_ = rp.SetEntityValue(entityID, start, nil)
	t := time.NewTicker(interval)
	go func() {
		defer t.Stop()
		val := start
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				val = !val
				if err := rp.SetEntityValue(entityID, val, nil); err != nil {
					log.Println("strobe:", err)
				}
			}
		}
	}()
}

func (rp *RustPlus) SendTeamMessage(message string, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		SendTeamMessage: &AppSendMessage{
			Message: proto.String(message),
		},
	}, cb)
}

func (rp *RustPlus) GetEntityInfo(entityID uint32, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		EntityId:      proto.Uint32(entityID),
		GetEntityInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetMap(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetMap: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetTime(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetTime: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetMapMarkers(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetMapMarkers: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetInfo(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetTeamInfo(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetTeamInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) SubscribeToCamera(identifier string, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraSubscribe: &AppCameraSubscribe{
			CameraId: proto.String(identifier),
		},
	}, cb)
}

func (rp *RustPlus) UnsubscribeFromCamera(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraUnsubscribe: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) SendCameraInput(buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraInput: &AppCameraInput{
			Buttons:    proto.Int32(buttons),
			MouseDelta: &Vector2{X: &dx, Y: &dy},
		},
	}, cb)
}

func (rp *RustPlus) BotSay(msg string) error {
	err := rp.SendTeamMessage("[bot] "+msg, nil)
	return err
}

// ========================= удобный враппер Camera =========================

type Camera struct {
	rp  *RustPlus
	id  string
	sub bool
}

func (rp *RustPlus) GetCamera(identifier string) *Camera {
	return &Camera{rp: rp, id: identifier}
}

func (c *Camera) Subscribe(cb func(*AppMessage) bool) error {
	if err := c.rp.SubscribeToCamera(c.id, cb); err != nil {
		return err
	}
	c.sub = true
	return nil
}

func (c *Camera) Unsubscribe(cb func(*AppMessage) bool) error {
	if !c.sub {
		return nil
	}
	c.sub = false
	return c.rp.UnsubscribeFromCamera(cb)
}

func (c *Camera) Input(buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return c.rp.SendCameraInput(buttons, dx, dy, cb)
}

// формирует адрес ws/wss по текущей конфигурации
func (rp *RustPlus) wsURL() string {
	if rp.useProxy {
		return fmt.Sprintf("wss://companion-rust.facepunch.com/game/%s/%d", rp.server, rp.port)
	}
	return fmt.Sprintf("ws://%s:%d", rp.server, rp.port)
}

// dial с установкой pong‑handler’а, дедлайнов и запуском пингов
func (rp *RustPlus) dialAndSetup() (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(rp.wsURL(), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)

	// всегда обновляем отметку активности сразу
	rp.touchActivity()

	if rp.useProxy {
		// через Facepunch proxy — pong есть
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		conn.SetPongHandler(func(string) error {
			rp.touchActivity()
			return conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		})
		rp.startPing(conn) // ping каждые 10s
	} else {
		// прямой коннект к серверу: pong обычно нет
		// дедлайн НЕ ставим — будем держать соединение app‑heartbeat'ом
		rp.stopPing()
		rp.startAppHeartbeat()
	}
	return conn, nil
}

func (rp *RustPlus) startPing(c *websocket.Conn) {
	rp.stopPing() // на всякий — останавливаем предыдущие
	rp.pingStop = make(chan struct{})

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				rp.wmu.Lock()
				_ = c.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
				rp.wmu.Unlock()
			case <-rp.pingStop:
				return
			}
		}
	}()
}

func (rp *RustPlus) stopPing() {
	if rp.pingStop != nil {
		close(rp.pingStop)
		rp.pingStop = nil
	}
}

// безопасно закрыть текущее соединение
func (rp *RustPlus) closeConn() {
	rp.stopPing() // останавливает и app-heartbeat, и ws-ping (мы их делим одним каналом)
	if rp.conn != nil {
		_ = rp.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closing"),
			time.Now().Add(500*time.Millisecond))
		_ = rp.conn.Close()
		rp.conn = nil
	}
}

// пометить все ожидающие callbacks ошибкой при реконнекте/закрытии
func (rp *RustPlus) failPendingCallbacks(err error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.cbs) == 0 {
		return
	}
	for k := range rp.cbs {
		cb := rp.cbs[k]
		// оборачиваем в искусственный AppMessage с AppResponse.Error
		if cb != nil {
			msg := &AppMessage{
				Response: &AppResponse{
					Error: &AppError{Error: proto.String(err.Error())},
				},
			}
			// вызвать cb, но он «съест» сообщение и мы тут ничего не ждём
			cb(msg)
		}
		delete(rp.cbs, k)
	}
}

func (rp *RustPlus) touchActivity() {
	rp.lastActivity.Store(time.Now().UnixNano())
}

func (rp *RustPlus) sinceLastActivity() time.Duration {
	n := rp.lastActivity.Load()
	if n == 0 {
		return time.Hour
	}
	return time.Since(time.Unix(0, n))
}

func (rp *RustPlus) startAppHeartbeat() {
	rp.stopPing() // на всякий
	rp.pingStop = make(chan struct{})

	go func() {
		tick := time.NewTicker(25 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-rp.pingStop:
				return
			case <-tick.C:
				// если давно не было трафика — пульнём GetMapMarkers
				if rp.sinceLastActivity() > 20*time.Second && rp.conn != nil {
					// маленький таймаут ожидания
					done := make(chan struct{}, 1)
					_ = rp.GetMapMarkers(func(m *AppMessage) bool {
						done <- struct{}{}
						return true
					})
					select {
					case <-done:
						rp.touchActivity()
					case <-time.After(8 * time.Second):
						// считаем соединение подвисшим — закрываем, readLoop реконнектит
						rp.closeConn()
					}
				}
			}
		}
	}()
}
