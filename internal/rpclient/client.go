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
	PlayerID      uint64 `json:"player_id"`
	PlayerToken   int32  `json:"player_token"`
	ServerID      string `json:"server_id,omitempty"`
	AuthToken     string `json:"auth_token,omitempty"`
	AuthTokenFile string `json:"auth_token_file,omitempty"`
}

type RustPlus struct {
	server        string
	port          int
	playerID      uint64
	playerToken   int32
	useProxy      bool
	serverID      string
	authToken     string
	authTokenFile string

	serverInfo  *AppInfo
	lifecycleMu sync.Mutex
	connMu      sync.Mutex
	conn        *websocket.Conn
	cancel      context.CancelFunc
	running     bool
	seq         uint32
	mu          sync.Mutex
	cbs         map[uint32]func(*AppMessage) bool
	wmu         sync.Mutex

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

func (rp *RustPlus) nextSeq() uint32 {
	return atomic.AddUint32(&rp.seq, 1)
}

// Connect validates a Rust+ reply before publishing the connection.
func (rp *RustPlus) Connect(ctx context.Context) error {
	return rp.ConnectWithTimeout(ctx, 10*time.Second)
}

// The timeout bounds only this attempt, not the lifetime of a successful connection.
func (rp *RustPlus) ConnectWithTimeout(ctx context.Context, timeout time.Duration) error {
	rp.lifecycleMu.Lock()
	defer rp.lifecycleMu.Unlock()

	rp.connMu.Lock()
	running := rp.running
	rp.connMu.Unlock()

	if running {
		return errors.New("Rust+ client is already running")
	}

	if rp.OnConnecting != nil {
		rp.OnConnecting()
	}

	attempt, stop := context.WithTimeout(ctx, timeout)
	defer stop()

	conn, err := rp.dialAndSetup(attempt)
	if err != nil {
		return err
	}

	lifetime, cancel := context.WithCancel(ctx)

	rp.connMu.Lock()
	if lifetime.Err() != nil {
		rp.connMu.Unlock()

		cancel()
		conn.Close()
		return lifetime.Err()
	}

	rp.conn, rp.cancel, rp.running = conn, cancel, true
	rp.connMu.Unlock()

	go rp.readLoop(lifetime, conn)

	if rp.OnConnected != nil {
		rp.OnConnected()
	}

	return nil
}

func (rp *RustPlus) Disconnect() {
	rp.connMu.Lock()
	cancel := rp.cancel
	rp.connMu.Unlock()

	if cancel != nil {
		cancel()
	}

	rp.closeConn()
}

func (rp *RustPlus) IsConnected() bool {
	rp.connMu.Lock()
	defer rp.connMu.Unlock()

	return rp.conn != nil
}

func (rp *RustPlus) SendRequest(req *AppRequest, cb func(*AppMessage) bool) error {
	rp.connMu.Lock()
	conn := rp.conn
	rp.connMu.Unlock()

	return rp.sendRequestOn(conn, req, cb)
}

func (rp *RustPlus) sendRequestOn(conn *websocket.Conn, req *AppRequest, cb func(*AppMessage) bool) error {
	if req == nil {
		return errors.New("nil Rust+ request")
	}

	seq := rp.nextSeq()
	req.Seq, req.PlayerId, req.PlayerToken = &seq, &rp.playerID, &rp.playerToken

	data, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	rp.connMu.Lock()
	if conn == nil || rp.conn != conn {
		rp.connMu.Unlock()

		return errors.New("Rust+ is not connected")
	}

	if cb != nil {
		rp.mu.Lock()
		rp.cbs[seq] = cb
		rp.mu.Unlock()
	}

	rp.connMu.Unlock()

	if rp.OnRequest != nil {
		rp.OnRequest(req)
	}

	rp.wmu.Lock()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = conn.WriteMessage(websocket.BinaryMessage, data)
	rp.wmu.Unlock()

	if err != nil {
		rp.removeCallback(seq)
		conn.Close() // Wake the reader and reconnect after a failed write.
	}

	return err
}

func (rp *RustPlus) removeCallback(seq uint32) {
	rp.mu.Lock()
	delete(rp.cbs, seq)
	rp.mu.Unlock()
}

func (rp *RustPlus) SendRequestAsync(req *AppRequest, timeout time.Duration) (*AppResponse, error) {
	responses := make(chan *AppResponse, 1)
	err := rp.SendRequest(req, func(m *AppMessage) bool {
		responses <- m.GetResponse()
		return true
	})
	if err != nil {
		return nil, err
	}

	defer rp.removeCallback(req.GetSeq())

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-responses:
		if r == nil {
			return nil, errors.New("empty Rust+ response")
		}

		if r.Error != nil {
			return nil, errors.New(r.Error.GetError())
		}

		return r, nil
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for Rust+ response after %v", timeout)
	}
}

// ServerInfo is the immutable snapshot obtained by the most recent connection verification.
func (rp *RustPlus) ServerInfo() *AppInfo {
	rp.connMu.Lock()
	defer rp.connMu.Unlock()

	if rp.serverInfo == nil {
		return nil
	}

	return proto.Clone(rp.serverInfo).(*AppInfo)
}
