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

	alarms    map[uint32]smartAlarm
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

	// death-watch
	dwMu      sync.Mutex
	dwRunning bool
	dwCancel  context.CancelFunc
	dwEvery   time.Duration
}

func New() *RustPlusBot {
	return &RustPlusBot{
		alarms: make(map[uint32]smartAlarm),
	}
}

func (bot *RustPlusBot) SetCheckPlayerDeath(steamID uint64, sound *string) {
	death := playerDeath{steamID: steamID}
	bot.checkPlayerDeath = &death
	if sound != nil {
		bot.checkPlayerDeath.callback = bot.callbackForSound(*sound)
	}
}

func (bot *RustPlusBot) SetRustPlusClient(cfg rpclient.RustPlusConfig) {
	bot.rpc = rpclient.New(cfg.Server, cfg.Port, cfg.PlayerID, cfg.PlayerToken, cfg.UseProxy)

	bot.rpc.OnConnecting = func() { fmt.Println("connecting...") }

	// КЛЮЧЕВОЕ: любое успешное подключение (первое или реконнект) — делаем re-init
	bot.rpc.OnConnected = func() {
		fmt.Println("connected")
		go bot.reinitDevices()
	}

	bot.rpc.OnError = func(err error) { fmt.Println("err:", err) }

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
				if alarm.callback != nil {
					go alarm.callback()
				}
			}
		}
	}
}

func (bot *RustPlusBot) SetMediaHook() {
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
		panic(err)
	}
	bot.mediaHook = h
}

func (bot *RustPlusBot) SetBattleMetrics(cfg bmapi.BMConf) {
	bm := bmapi.NewClientFromConf(cfg)
	bot.bm = bm
}

func (bot *RustPlusBot) SetSwitch(number int, switchId uint32, switchName string) error {
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

func (bot *RustPlusBot) SetAlarm(alarmId uint32, alarmName, alarmMsg string, triggerFunc func()) {
	bot.alarms[alarmId] = smartAlarm{name: alarmName, msg: alarmMsg, callback: triggerFunc}
	bot.initAlarmByID(alarmId)
}

func (bot *RustPlusBot) Start() error {
	if bot == nil {
		return errors.New("бот не инициализирован")
	}
	if bot.rpc == nil {
		return errors.New("модуль rpc не инициализирован")
	}
	if bot.stopCh != nil {
		return errors.New("уже запущен")
	}
	bot.stopCh = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	if err := bot.rpc.Connect(ctx); err != nil {
		cancel()
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
		<-bot.stopCh
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
	ch := bot.stopCh
	bot.stopCh = nil
	bot.mu.Unlock()

	if ch != nil {
		close(ch)     // безопасно: повторный Stop() ничего не делает
		bot.wg.Wait() // дождёмся остановки фонового горутины
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
