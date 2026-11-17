package rpclient

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

func (rp *RustPlus) readLoop(ctx context.Context, conn *websocket.Conn) {
	watcherDone := make(chan struct{})
	stopWatcher := context.AfterFunc(ctx, func() {
		rp.closeConn()
		close(watcherDone)
	})

	defer func() {
		if !stopWatcher() {
			<-watcherDone
		}

		rp.closeConn()
		rp.failPendingCallbacks(fmt.Errorf("Rust+ disconnected"))
		rp.connMu.Lock()
		rp.running, rp.cancel = false, nil
		rp.connMu.Unlock()
	}()

	backoff := time.Second

	for {
		session, stopHeartbeat := context.WithCancel(ctx)
		rp.startHeartbeat(session, conn)

		err := rp.readMessages(conn)
		stopHeartbeat()

		rp.closeConn()
		rp.failPendingCallbacks(fmt.Errorf("Rust+ connection lost"))

		if rp.OnDisconnected != nil {
			rp.OnDisconnected()
		}

		if ctx.Err() != nil {
			return
		}

		if rp.OnError != nil {
			rp.OnError(err)
		}

		for {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			if rp.OnConnecting != nil {
				rp.OnConnecting()
			}

			next, err := rp.dialAndSetup(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}

				if rp.OnError != nil {
					rp.OnError(fmt.Errorf("reconnect failed: %w; next attempt in %v", err, min(2*backoff, 30*time.Second)))
				}

				backoff = min(2*backoff, 30*time.Second)
				continue
			}

			rp.connMu.Lock()
			if ctx.Err() != nil {
				rp.connMu.Unlock()

				next.Close()
				return
			}

			rp.conn = next
			rp.connMu.Unlock()

			conn = next
			backoff = time.Second

			if rp.OnConnected != nil {
				go rp.OnConnected()
			}

			break
		}
	}
}

func (rp *RustPlus) readMessages(conn *websocket.Conn) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		var msg AppMessage
		// The live companion route omits default-valued proto2 fields (e.g. queuedPlayers=0).
		// Decode those as defaults while still rejecting malformed wire data.
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(data, &msg); err != nil {
			if rp.OnError != nil {
				rp.OnError(err)
			}

			continue
		}

		if resp := msg.GetResponse(); resp != nil && resp.Seq != nil {
			rp.mu.Lock()
			cb := rp.cbs[resp.GetSeq()]
			delete(rp.cbs, resp.GetSeq())
			rp.mu.Unlock()

			if cb != nil && cb(&msg) {
				continue
			}
		}

		if rp.OnMessage != nil {
			rp.OnMessage(&msg)
		}
	}
}

// Invoke user callbacks after releasing the lock so they can safely send another request.
func (rp *RustPlus) failPendingCallbacks(err error) {
	rp.mu.Lock()
	pending := rp.cbs
	rp.cbs = make(map[uint32]func(*AppMessage) bool)
	rp.mu.Unlock()

	for seq, cb := range pending {
		if cb != nil {
			cb(&AppMessage{Response: &AppResponse{Seq: proto.Uint32(seq), Error: &AppError{Error: proto.String(err.Error())}}})
		}
	}
}
