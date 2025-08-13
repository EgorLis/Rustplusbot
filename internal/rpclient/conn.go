package rpclient

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ========================= low-level =========================

func (rp *RustPlus) nextSeq() uint32 {
	return atomic.AddUint32(&rp.seq, 1)
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
	conn.SetReadLimit(64 << 20)

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
					_ = rp.GetTeamInfo(func(m *AppMessage) bool {
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
