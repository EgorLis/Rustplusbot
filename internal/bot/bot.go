package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bluetooth"
	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/rustplus"
	"github.com/EgorLis/Rustplusbot/internal/tools"
)

var logger = log.New(os.Stdout, "[bot] ", log.LstdFlags)

const (
	requestTimeout = 5 * time.Second
)

type RustPlusBot struct {
	bm        *bmapi.Client
	rpc       *rustplus.Client
	bluetooth *bluetooth.BluetoothHandler

	alarms    map[uint32]smartAlarm
	bt1switch *smartSwitch
	bt2switch *smartSwitch

	checkPlayerDeath *playerDeath

	cfg *configStore

	globalCtx    context.Context
	globalCancel context.CancelFunc
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.Mutex

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

	circularBufferLogs *tools.CircularBuffer
}

func New() *RustPlusBot {
	globaclCtx, globalCancel := context.WithCancel(context.Background())

	return &RustPlusBot{
		alarms:       make(map[uint32]smartAlarm),
		globalCtx:    globaclCtx,
		globalCancel: globalCancel,
	}
}

func (bot *RustPlusBot) UseCircularBufferLogs(bufferSize int) {
	bot.circularBufferLogs = tools.NewCircularBuffer(bufferSize)

	logger = log.New(bot.circularBufferLogs, "[bot] ", log.LstdFlags)
}

func (bot *RustPlusBot) SetCheckPlayerDeath(steamID uint64, sound *string) {
	death := playerDeath{steamID: steamID}
	bot.checkPlayerDeath = &death
	if sound != nil {
		bot.checkPlayerDeath.callback = bot.callbackForSound(*sound)
	}
}

func (bot *RustPlusBot) SetRustPlusClient(cfg rustplus.Config) {
	onConnecting := func() { logger.Println("connecting...") }

	// КЛЮЧЕВОЕ: любое успешное подключение (первое или реконнект) — делаем re-init
	onConnected := func() {
		logger.Println("connected")
		ctx, _ := bot.getCtx()
		go bot.reinitDevices(ctx)
	}

	onError := func(err error) { logger.Println("err:", err) }

	onMessage := func(msg *rustplus.AppMessage) {
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
			logger.Printf("[%s] %s", playerName, text)
			if strings.HasPrefix(text, "!") {
				if err := bot.HandleCommand(text); err != nil {
					ctx, _ := bot.getCtx()
					bot.rpc.BotSay(ctx, fmt.Sprintf("err: %v", err))
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
				ctx, _ := bot.getCtx()
				bot.rpc.BotSay(ctx, text)
				if alarm.callback != nil && !bot.amIsMuted {
					go alarm.callback()
				}
			}
		}
	}

	bot.rpc = rustplus.NewClient(&cfg, rustplus.Events{
		OnConnecting: onConnecting,
		OnConnected:  onConnected,
		OnError:      onError,
		OnMessage:    onMessage,
	})
}

func (bot *RustPlusBot) SetMediaHook() {
	h := bluetooth.NewBluetoothHandler()
	h.SetCallbacks(func() {
		ctx, _ := bot.getCtx()
		logger.Println("UP pressed")
		msg := bot.turnSwitch(ctx, 1)
		bot.rpc.BotSay(ctx, msg)
	},
		func() {
			ctx, _ := bot.getCtx()
			logger.Println("DOWN pressed")
			msg := bot.turnSwitch(ctx, 2)
			bot.rpc.BotSay(ctx, msg)
		})

	bot.bluetooth = h
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
	ctx, _ := bot.getCtx()
	bot.initAlarmByID(ctx, alarmId)
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

	bot.rpc.Run()
	if bot.circularBufferLogs != nil {
		bot.rpc.UseCircularBufferLogs(bot.circularBufferLogs)
	}

	if bot.bm != nil {
		if bot.circularBufferLogs != nil {
			bot.bm.UseCircularBufferLogs(bot.circularBufferLogs)
		}
		notify := func(text string) {
			ctx, _ := bot.getCtx()
			_ = bot.rpc.BotSay(ctx, text)
		}
		_ = bot.bm.StartScan(1*time.Minute, notify)
	}

	if bot.bluetooth != nil {
		if bot.circularBufferLogs != nil {
			bot.bluetooth.UseCircularBufferLogs(bot.circularBufferLogs)
		}
		if err := bot.bluetooth.Start(); err != nil {
			logger.Println("mediahook:", err)
		}
	}

	// сторож для остановки
	bot.wg.Go(func() {
		<-bot.stopCh

		bot.globalCancel() // отменяем все контексты

		logger.Println("Stop command was received, canceling global ctx, wait 5 sec")

		<-time.After(requestTimeout) // даём goroutines чуть времени на завершение после отмены контекста

		logger.Println("Stopping bot...")

		if bot.bluetooth != nil {
			err := bot.bluetooth.Stop()
			if err != nil {
				logger.Println("Error while stoppint bluetooth handler: ", err)
			}
		}
		if bot.bm != nil {
			bot.bm.Stop()
		}

		if bot.rpc != nil {
			bot.rpc.Stop()
		}

		logger.Println("Bot stopped")

	})

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
func (bot *RustPlusBot) reinitDevices(ctx context.Context) {
	// антидребезг: если OnConnected прилетело несколько раз подряд — коллапсируем в 1 вызов
	bot.reinitMu.Lock()
	if time.Since(bot.lastReinit) < 2*time.Second {
		bot.reinitMu.Unlock()
		return
	}
	bot.lastReinit = time.Now()
	bot.reinitMu.Unlock()

	// синхронизируем свитчи: читаем текущее значение и обновляем state (НЕ переключаем)
	bot.initSwitch(ctx, bot.bt1switch)
	bot.initSwitch(ctx, bot.bt2switch)

	// просто лог/проверка для алармов
	for id := range bot.alarms {
		bot.initAlarmByID(ctx, id)
	}
}

func (bot *RustPlusBot) getCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(bot.globalCtx, requestTimeout)
}

func (bot *RustPlusBot) GetRPC() *rustplus.Client {
	return bot.rpc
}

func (bot *RustPlusBot) GetBM() *bmapi.Client {
	return bot.bm
}

func (bot *RustPlusBot) GetCircularBuffer() *tools.CircularBuffer {
	return bot.circularBufferLogs
}
