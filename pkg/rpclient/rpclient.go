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
	addr := fmt.Sprintf("ws://%s:%d", rp.server, rp.port)
	if rp.useProxy {
		addr = fmt.Sprintf("wss://companion-rust.facepunch.com/game/%s/%d", rp.server, rp.port)
	}
	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
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
	if rp.conn != nil {
		_ = rp.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(1*time.Second))
		_ = rp.conn.Close()
		rp.conn = nil
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
		if rp.OnDisconnected != nil {
			rp.OnDisconnected()
		}
	}()

	// доп. горутина: по отмене контекста закрыть сокет, чтобы ReadMessage проснулся
	go func() {
		<-ctx.Done()
		if rp.conn != nil {
			_ = rp.conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "ctx cancel"),
				time.Now().Add(500*time.Millisecond))
			_ = rp.conn.Close()
		}
	}()

	for {
		_, data, err := rp.conn.ReadMessage()
		if err != nil {
			if rp.OnError != nil && !rp.closed.Load() {
				rp.OnError(err)
			}
			return
		}

		var msg AppMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			if rp.OnError != nil {
				rp.OnError(err)
			}
			continue
		}

		// коллбек по seq
		if resp := msg.GetResponse(); resp != nil {
			if resp.Seq != nil {
				seq := *resp.Seq
				rp.mu.Lock()
				cb, ok := rp.cbs[seq]
				if ok {
					delete(rp.cbs, seq)
				}
				rp.mu.Unlock()

				if ok {
					// Если cb вернёт true — событие "message" не эмитим
					if cb(&msg) {
						continue
					}
				}
			}
		}

		if rp.OnMessage != nil {
			rp.OnMessage(&msg)
		}
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

	// (необязательно) дедлайн на запись, чтобы не зависнуть
	_ = rp.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if rp.OnRequest != nil {
		rp.OnRequest(req)
	}
	return rp.conn.WriteMessage(websocket.BinaryMessage, data)
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
