package rustplus

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/tools"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

var logger = log.New(os.Stdout, "[rustplus] ", log.LstdFlags)

type Events struct {
	OnConnecting   func()
	OnConnected    func()
	OnMessage      func(*AppMessage)
	OnDisconnected func()
	OnError        func(error)
	OnRequest      func(*AppRequest)
}

type Config struct {
	Server      string `json:"server"`
	Port        int    `json:"port"`
	PlayerID    uint64 `json:"player_id"`
	PlayerToken int32  `json:"player_token"`
	UseProxy    bool   `json:"use_proxy"`
}

type Client struct {
	conf *Config

	taskProcessingChan chan task
	disconnectedChan   chan struct{}

	stop context.CancelFunc
	wg   sync.WaitGroup

	events Events
}

type connection struct {
	websocket *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
}

type task struct {
	Request *AppRequest
	Cb      func(*AppMessage) bool
}

type callback struct {
	fn  func(*AppMessage) bool
	seq uint32
}

func NewClient(config *Config, events Events) *Client {
	return &Client{
		conf:               config,
		events:             events,
		taskProcessingChan: make(chan task),
		disconnectedChan:   make(chan struct{}),
	}
}

func (c *Client) UseCircularBufferLogs(circularBufferLogs *tools.CircularBuffer) {
	logger = log.New(circularBufferLogs, "[rustplus] ", log.LstdFlags)
}

func (c *Client) Run() {
	messages := make(chan *AppMessage)
	callbacks := make(chan callback)

	tasksConnections := make(chan *connection)
	readConnections := make(chan *connection)

	runCtx, cancel := context.WithCancel(context.Background())
	c.stop = cancel

	c.wg.Go(func() {
		c.handleConnections(runCtx, tasksConnections, readConnections)
	})

	c.wg.Go(func() {
		c.startReadLoop(runCtx, readConnections, messages)
	})

	c.wg.Go(func() {
		c.handleTasks(runCtx, tasksConnections, c.taskProcessingChan, callbacks)
	})

	c.wg.Go(func() {
		c.handleMessages(runCtx, callbacks, messages)
	})
}

func (c *Client) Stop() {
	c.stop()
	logger.Println("Stopping client, waiting for goroutines to finish...")
	c.wg.Wait()
}

func (c *Client) addTask(ctx context.Context, task task) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("failed to add task: %w", ctx.Err())
	case c.taskProcessingChan <- task:
		return nil
	}
}

func (c *Client) handleConnections(ctx context.Context, connections ...chan<- *connection) {
	connect := func() {
		conn, connected := c.connectWithBackoff(ctx)

		if !connected {
			logger.Println("Failed to reconnect, stopping connection handler.")
			return
		}

		writeChannels(ctx, conn, connections)
	}

	// initial connection
	connect()

	for {
		select {
		case <-ctx.Done():
			logger.Println("Stopping connection handler")
			return
		case <-c.disconnectedChan:

			if c.events.OnDisconnected != nil {
				c.events.OnDisconnected()
			}

			logger.Println("Connection lost. Attempting to reconnect...")
			connect()
		}
	}
}

func (c *Client) startReadLoop(ctx context.Context, connections chan *connection, messages chan<- *AppMessage) {
	for {
		select {
		case <-ctx.Done():
			logger.Println("Stopping read loop")
			return
		case conn := <-connections:
		loop:
			for {
				select {
				case <-conn.ctx.Done():
					logger.Println("Stopping read loop")
					return
				default:
					_, data, err := conn.websocket.Read(conn.ctx)

					if err != nil {
						logger.Printf("Read error: %v\n", err)
						// если ошибка чтения — считаем соединение потерянным, закрываем и фейлим ожидающие
						_ = conn.websocket.Close(websocket.StatusAbnormalClosure, "read failed")

						select {
						case c.disconnectedChan <- struct{}{}: // сигнал о том, что нужно фейлить ожидающие
							conn.cancel() // отменяем контекст соединения, чтобы остановить все операции, связанные с этим соединением
						default:
						}

						break loop
					}

					var msg AppMessage
					if uerr := proto.Unmarshal(data, &msg); uerr != nil {
						if c.events.OnError != nil {
							c.events.OnError(uerr)
						}
						continue
					}

					messages <- &msg
				}
			}

		}
	}
}

func (c *Client) handleTasks(ctx context.Context, connections <-chan *connection, tasks <-chan task, callbacks chan<- callback) {
	seq := uint32(1)
	for {
		select {
		case <-ctx.Done():
			logger.Println("Stopping task handler")
			return
		case conn := <-connections:
		loop:
			for {
				select {
				case <-conn.ctx.Done():
					logger.Println("Stopping task loop")
					break loop
				case task := <-tasks:
					task.Request.Seq = &seq
					task.Request.PlayerId = &c.conf.PlayerID
					task.Request.PlayerToken = &c.conf.PlayerToken

					data, err := proto.Marshal(task.Request)

					if err != nil {
						logger.Printf("Failed to marshal request: %v\n", err)
						continue
					}

					if c.events.OnRequest != nil {
						c.events.OnRequest(task.Request)
					}

					if task.Cb != nil {
						callbacks <- callback{fn: task.Cb, seq: seq}
					}

					reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()

					err = conn.websocket.Write(reqCtx, websocket.MessageBinary, data)
					if err != nil {
						logger.Printf("Failed to write request: %v", err)
						_ = conn.websocket.Close(websocket.StatusAbnormalClosure, "write failed")

						select {
						case c.disconnectedChan <- struct{}{}: // сигнал о том, что нужно фейлить ожидающие
							conn.cancel() // отменяем контекст соединения, чтобы остановить все операции, связанные с этим соединением
						default:
						}

						break loop
					}

					seq++

					// небольшая задержка перед следующей записью
					select {
					case <-conn.ctx.Done():
						logger.Println("Stopping task loop")
						break loop
					case <-time.After(100 * time.Millisecond):
					}
				}
			}

		}
	}
}

func (c *Client) handleMessages(ctx context.Context, callbacks <-chan callback, messages <-chan *AppMessage) {
	callbackMap := make(map[uint32]func(*AppMessage) bool)

	for {
		select {
		case cb := <-callbacks:
			callbackMap[cb.seq] = cb.fn
		default:
		}

		select {
		case <-ctx.Done():
			logger.Println("Stopping callback handler")
			return
		case msg := <-messages:
			// callbacks по seq
			if resp := msg.GetResponse(); resp != nil && resp.Seq != nil {

				if resp.Error != nil && c.events.OnError != nil {
					c.events.OnError(fmt.Errorf("server error response: %s", resp.Error.GetError()))
				}

				seq := *resp.Seq

				cb, ok := callbackMap[seq]
				if ok {
					delete(callbackMap, seq)
				}

				if ok && cb(msg) {
					continue
				}
			}

			if c.events.OnMessage != nil {
				c.events.OnMessage(msg)
			}
		case cb := <-callbacks:
			callbackMap[cb.seq] = cb.fn
		}
	}
}

func (c *Client) connectWithBackoff(ctx context.Context) (*connection, bool) {
	// Простейший экспоненциальный бэкофф
	var backoff time.Duration = 1 * time.Second
	var maxBackoff time.Duration = 30 * time.Second

	if c.events.OnConnecting != nil {
		c.events.OnConnecting()
	}

	connectionString := c.wsURL()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, connectionString, &websocket.DialOptions{})

	for err != nil {
		logger.Printf("Connection failed: %v. Retrying in %v...\n", err, backoff)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(backoff):
			dialCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			conn, _, err = websocket.Dial(dialCtx, connectionString, &websocket.DialOptions{})
			cancel()
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}

	// Устанавливаем лимит на размер сообщения (по умолчанию 32768 байт)
	conn.SetReadLimit(10 * 1024 * 1024) // 10MB

	if c.events.OnConnected != nil {
		c.events.OnConnected()
	}

	connectCtx, cancel := context.WithCancel(ctx)

	return &connection{websocket: conn, ctx: connectCtx, cancel: cancel}, true
}

func (c *Client) wsURL() string {
	if c.conf.UseProxy {
		return fmt.Sprintf("wss://companion-rust.facepunch.com/game/%s/%d", c.conf.Server, c.conf.Port)
	}
	return fmt.Sprintf("ws://%s:%d", c.conf.Server, c.conf.Port)
}

func writeChannels[T any](ctx context.Context, val T, channels []chan<- T) {
	for _, ch := range channels {
		select {
		case <-ctx.Done():
			return
		case ch <- val:
		}
	}
}
