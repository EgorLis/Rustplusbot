package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/mediahook"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type RustPlusBot struct {
	bm        *bmapi.Client
	rpc       *rpclient.RustPlus
	mediaHook *mediahook.Hook

	alarms    map[uint64]smartAlarm
	bt1switch *smartSwitch
	bt2switch *smartSwitch

	checkPlayerDeath *playerDeath

	cfg *configStore

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex

	// чтобы не дёргать re-init слишком часто при серии быстрых реконнектов
	reinitMu   sync.Mutex
	lastReinit time.Time

	// Alarm mute (не воспроизводить звуки, если это не нужно)
	amMu      sync.Mutex
	amIsMuted bool

	// death-watch
	dwMu      sync.Mutex
	dwRunning bool
	dwCancel  context.CancelFunc
	dwEvery   time.Duration
}

func New() *RustPlusBot {
	return &RustPlusBot{
		alarms: make(map[uint64]smartAlarm),
	}
}

func (bot *RustPlusBot) SetCheckPlayerDeath(steamID uint64, sound *string) {
	death := playerDeath{steamID: steamID}
	bot.checkPlayerDeath = &death
	if sound != nil {
		bot.checkPlayerDeath.callback = bot.callbackForSound(*sound)
	}
}

func (bot *RustPlusBot) SetRustPlusClient(cfg rpclient.RustPlusConfig) error {
	rpc, err := rpclient.NewFromConfig(cfg)
	if err != nil {
		return err
	}

	bot.rpc = rpc

	bot.rpc.OnConnecting = func() {
		log.Println("Rust+: connecting...")
	}

	// КЛЮЧЕВОЕ: любое успешное подключение (первое или реконнект) — делаем re-init
	bot.rpc.OnConnected = func() {
		log.Printf("Rust+: connected to %q; server verified", bot.rpc.ServerInfo().GetName())

		go bot.reinitDevices()
	}

	bot.rpc.OnDisconnected = func() {
		log.Println("Rust+: disconnected")
	}

	bot.rpc.OnError = func(err error) {
		log.Println("Rust+:", err)
	}

	bot.rpc.OnMessage = func(msg *rpclient.AppMessage) {
		b := msg.GetBroadcast()
		if b == nil {
			return
		}

		// --- чат-команды ---
		if chat := b.GetTeamMessage(); chat != nil {
			message := chat.GetMessage()
			text := strings.TrimSpace(message.GetMessage())
			if strings.HasPrefix(text, "[bot]") {
				return
			}

			playerName := message.GetName()
			log.Printf("[%s] %s", playerName, text)
			if strings.HasPrefix(text, "!") {
				if err := bot.HandleCommand(text); err != nil {
					bot.rpc.BotSay(fmt.Sprintf("err: %v", err))
				}

				return
			}
		}

		// --- smart alarm ---
		if ec := b.GetEntityChanged(); ec != nil {
			id := ec.GetEntityId()
			alarm, watched := bot.alarms[id]
			if !watched {
				return
			}

			if p := ec.GetPayload(); p != nil && p.Value != nil && p.GetValue() {
				text := fmt.Sprintf("[ALARM TRIGGERED] %s (%d): %s", alarm.name, id, alarm.msg)
				bot.rpc.BotSay(text)
				if alarm.callback != nil && !bot.amIsMuted {
					go alarm.callback()
				}
			}
		}
	}

	return nil
}

func (bot *RustPlusBot) SetMediaHook() error {
	h, err := mediahook.New(
		func() {
			log.Println("UP pressed")
			msg := bot.turnSwitch(1)
			bot.rpc.BotSay(msg)
		},
		func() {
			log.Println("DOWN pressed")
			msg := bot.turnSwitch(2)
			bot.rpc.BotSay(msg)
		},
	)
	if err != nil {
		return err
	}

	bot.mediaHook = h
	return nil
}

func (bot *RustPlusBot) SetBattleMetrics(cfg bmapi.BMConf) {
	bm := bmapi.NewClientFromConf(cfg)
	bot.bm = bm
}

func (bot *RustPlusBot) SetSwitch(number int, switchId uint64, switchName string) error {
	if number < 1 || number > 2 {
		return errors.New("такую кнопку нельзя установить")
	}

	sw := &smartSwitch{id: switchId, name: switchName, state: false}

	if number == 1 {
		bot.bt1switch = sw
	} else {
		bot.bt2switch = sw
	}

	return nil
}

func (bot *RustPlusBot) SetAlarm(alarmId uint64, alarmName, alarmMsg string, triggerFunc func()) {
	bot.alarms[alarmId] = smartAlarm{name: alarmName, msg: alarmMsg, callback: triggerFunc}
	bot.initAlarmByID(alarmId)
}

func (bot *RustPlusBot) Start() error {
	return bot.StartContext(context.Background())
}

// StartContext bounds startup to one minute. A successful connection keeps the parent lifetime.
func (bot *RustPlusBot) StartContext(parent context.Context) error {
	if bot == nil || bot.rpc == nil {
		return errors.New("Rust+ client is not configured")
	}

	bot.mu.Lock()
	if bot.stopCh != nil {
		bot.mu.Unlock()

		return errors.New("bot is already running")
	}

	stopCh := make(chan struct{})
	bot.stopCh = stopCh
	bot.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)

	if err := bot.connectInitial(ctx, time.Minute); err != nil {
		cancel()

		bot.mu.Lock()
		bot.stopCh = nil
		bot.mu.Unlock()

		return err
	}

	if bot.bm != nil {
		notify := func(text string) {
			_ = bot.rpc.BotSay(text)
		}

		_ = bot.bm.StartScan(1*time.Minute, notify)
	}

	if bot.mediaHook != nil {
		if err := bot.mediaHook.Start(); err != nil {
			log.Println("mediahook:", err)
		}
	}

	// сторож для остановки
	bot.wg.Add(1)
	go func() {
		defer bot.wg.Done()

		<-stopCh
		if bot.mediaHook != nil {
			bot.mediaHook.Close()
		}

		if bot.bm != nil {
			bot.bm.Stop()
		}

		cancel()
		bot.rpc.Disconnect()
	}()

	return nil
}

func (bot *RustPlusBot) Stop() {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if bot.stopCh != nil {
		close(bot.stopCh)
		bot.wg.Wait()
		bot.stopCh = nil
	}
}

// connectInitial retries only inside the startup budget; transport reconnects own their lifetime.
func (bot *RustPlusBot) connectInitial(ctx context.Context, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	backoff := time.Second
	var lastErr error

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("Rust+ initial connection failed within %v: %w", budget, lastErr)
		}

		err := bot.rpc.ConnectWithTimeout(ctx, remaining)
		if err == nil {
			return nil
		}

		lastErr = err

		if ctx.Err() != nil {
			return ctx.Err()
		}

		var permanent *rpclient.PermanentError
		if errors.As(err, &permanent) {
			return err
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			continue
		}

		wait := min(backoff, remaining)
		log.Printf("Rust+ initial connection failed: %v; retrying in %v (startup limit %v)", err, wait.Round(time.Millisecond), budget)

		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		backoff = min(2*backoff, 30*time.Second)
	}
}

// re-init всех девайсов при (ре)подключении
func (bot *RustPlusBot) reinitDevices() {
	// антидребезг: если OnConnected прилетело несколько раз подряд — коллапсируем в 1 вызов
	bot.reinitMu.Lock()
	if time.Since(bot.lastReinit) < 2*time.Second {
		bot.reinitMu.Unlock()

		return
	}

	bot.lastReinit = time.Now()
	bot.reinitMu.Unlock()

	// синхронизируем свитчи: читаем текущее значение и обновляем state (НЕ переключаем)
	bot.initSwitch(bot.bt1switch)
	bot.initSwitch(bot.bt2switch)

	// просто лог/проверка для алармов
	for id := range bot.alarms {
		bot.initAlarmByID(id)
	}
}
