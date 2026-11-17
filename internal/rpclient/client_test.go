package rpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

func testClient(server *httptest.Server) *RustPlus {
	host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return New(host, port, 1, -123, false)
}

func infoReply(seq uint32) []byte {
	info := &AppInfo{Name: proto.String("test server"), HeaderImage: proto.String(""), Url: proto.String(""), Map: proto.String("test"), MapSize: proto.Uint32(4000), WipeTime: proto.Uint32(0), Players: proto.Uint32(1), MaxPlayers: proto.Uint32(100)}
	data, _ := (proto.MarshalOptions{AllowPartial: true}).Marshal(&AppMessage{Response: &AppResponse{Seq: proto.Uint32(seq), Info: info}})
	return data
}

func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out: " + what)
	}
}

func TestVerifiedConnectionReconnectsAndFailsPendingRequests(t *testing.T) {
	var upgrades atomic.Int32
	firstSocket := make(chan *websocket.Conn, 1)
	requestSeen := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if upgrades.Add(1) == 1 {
			firstSocket <- conn
		}
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request AppRequest
			if proto.Unmarshal(data, &request) != nil {
				return
			}
			if request.GetPlayerId() != 1 || request.GetPlayerToken() != -123 {
				t.Error("player credentials were not encoded correctly")
				return
			}
			if request.GetInfo != nil {
				if conn.WriteMessage(websocket.BinaryMessage, infoReply(request.GetSeq())) != nil {
					return
				}
			} else {
				requestSeen <- struct{}{}
			}
		}
	}))
	defer server.Close()
	rp := testClient(server)
	connected := make(chan struct{}, 8)
	rp.OnConnected = func() { connected <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rp.ConnectWithTimeout(ctx, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	await(t, connected, "initial connection")
	// A successful session must outlive the startup attempt deadline.
	time.Sleep(150 * time.Millisecond)
	if _, err := rp.SendRequestAsync(&AppRequest{GetInfo: &AppEmpty{}}, time.Second); err != nil {
		t.Fatal(err)
	}
	failed := make(chan error, 1)
	go func() {
		_, err := rp.SendRequestAsync(&AppRequest{GetTeamInfo: &AppEmpty{}}, 5*time.Second)
		failed <- err
	}()
	await(t, requestSeen, "pending request")
	(<-firstSocket).Close()
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("lost request did not fail")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request was left waiting for timeout")
	}
	await(t, connected, "reconnection after established socket loss")
	if _, err := rp.SendRequestAsync(&AppRequest{GetInfo: &AppEmpty{}}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Writes racing shutdown must return errors safely, never dereference a nil/new socket.
	var writers sync.WaitGroup
	for i := 0; i < 20; i++ {
		writers.Add(1)
		go func() { defer writers.Done(); _ = rp.GetInfo(nil) }()
	}
	rp.Disconnect()
	writers.Wait()
	cancel()
	time.Sleep(100 * time.Millisecond)
	count := upgrades.Load()
	time.Sleep(1100 * time.Millisecond)
	if rp.IsConnected() || upgrades.Load() != count {
		t.Fatal("disconnect allowed a reconnect")
	}
}

func TestHandshakeAloneDoesNotCountAsSuccessfulConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	rp := testClient(server)
	var connected atomic.Bool
	rp.OnConnected = func() { connected.Store(true) }
	started := time.Now()
	err := rp.ConnectWithTimeout(context.Background(), 100*time.Millisecond)
	if err == nil || connected.Load() || rp.IsConnected() {
		t.Fatal("unverified WebSocket was reported as connected")
	}
	if time.Since(started) > time.Second {
		t.Fatal("stalled verification ignored timeout")
	}
}

func TestRejectedPlayerTokenStopsStartup(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var request AppRequest
		if proto.Unmarshal(data, &request) != nil {
			return
		}
		reply, _ := proto.Marshal(&AppMessage{Response: &AppResponse{Seq: request.Seq, Error: &AppError{Error: proto.String("not_found")}}})
		conn.WriteMessage(websocket.BinaryMessage, reply)
	}))
	defer server.Close()
	rp := testClient(server)
	err := rp.Connect(context.Background())
	var permanent *PermanentError
	if !errors.As(err, &permanent) || rp.IsConnected() {
		t.Fatalf("expected a credential error, got %v", err)
	}
}

func TestAsyncTimeoutRemovesCallback(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request AppRequest
			if proto.Unmarshal(data, &request) != nil {
				return
			}
			if request.GetInfo != nil {
				conn.WriteMessage(websocket.BinaryMessage, infoReply(request.GetSeq()))
			}
		}
	}))
	defer server.Close()
	rp := testClient(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rp.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer rp.Disconnect()
	if _, err := rp.SendRequestAsync(&AppRequest{GetTeamInfo: &AppEmpty{}}, 30*time.Millisecond); err == nil {
		t.Fatal("expected timeout")
	}
	rp.mu.Lock()
	pending := len(rp.cbs)
	rp.mu.Unlock()
	if pending != 0 {
		t.Fatalf("timed out callback leaked: %d", pending)
	}
}

func TestFailPendingCallbackCanSendRequest(t *testing.T) {
	rp := New("localhost", 1, 1, 1, false)
	done := make(chan struct{})
	rp.cbs[1] = func(*AppMessage) bool { _ = rp.GetInfo(nil); close(done); return true }
	go rp.failPendingCallbacks(errors.New("lost"))
	await(t, done, "callback executed without holding the callback mutex")
}
