package rpclient

import (
	"context"
	"errors"
	"fmt"
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
