package rpclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
)

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
