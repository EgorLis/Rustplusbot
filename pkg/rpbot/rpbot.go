package rpbot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"example.com/volhook/pkg/bmapi"
	"example.com/volhook/pkg/mediahook"
	"example.com/volhook/pkg/rpclient"
)

type RustPlusBot struct {
	bm        *bmapi.Client
	rpc       *rpclient.RustPlus
	mediaHook *mediahook.Hook

	alarms    map[uint32]smartAlarm
	bt1switch *smartSwitch
	bt2switch *smartSwitch

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
}

type smartAlarm struct {
	name     string
	msg      string
	callback func()
}

type smartSwitch struct {
	sync.Mutex
	id    uint32
	name  string
	state bool
}

func New() *RustPlusBot {
	return &RustPlusBot{
		alarms: make(map[uint32]smartAlarm),
	}
}

func (bot *RustPlusBot) SetRustPlusClient(cfg rpclient.RustPlusConfig) {
	bot.rpc = rpclient.New(cfg.Server, cfg.Port, cfg.PlayerID, cfg.PlayerToken, cfg.UseProxy)

	bot.rpc.OnConnecting = func() { fmt.Println("connecting...") }
	bot.rpc.OnConnected = func() { fmt.Println("connected") }
	bot.rpc.OnError = func(err error) { fmt.Println("err:", err) }

	bot.rpc.OnMessage = func(msg *rpclient.AppMessage) {
		b := msg.GetBroadcast()
		if b == nil {
			return
		}

		// --- чат-команды ---
		if chat := b.GetTeamMessage(); chat != nil {
			tm := chat.GetMessage()
			text := strings.TrimSpace(tm.GetMessage())

			// игнорируем сообщения бота
			if strings.HasPrefix(text, "[bot]") {
				return
			}

			switch strings.ToLower(text) {
			case "!online":
				if bot.bm == nil {
					bot.rpc.BotSay("BattleMetrics API не подключён")
					return
				}
				onlineInfo := bot.bm.IsOnline()
				bot.rpc.BotSay(bot.bm.FormatOnlineInfo(onlineInfo))

			case "!bt1":
				if bot.bt1switch == nil {
					bot.rpc.BotSay("Кнопка 1: не инициализирована")
					return
				}
				bot.mu.Lock()
				bot.rpc.BotSay(fmt.Sprintf("Кнопка 1 - %s: %t", bot.bt1switch.name, bot.bt1switch.state))
				bot.mu.Unlock()

			case "!bt2":
				if bot.bt2switch == nil {
					bot.rpc.BotSay("Кнопка 2: не инициализирована")
					return
				}
				bot.mu.Lock()
				bot.rpc.BotSay(fmt.Sprintf("Кнопка 2 - %s: %t", bot.bt2switch.name, bot.bt2switch.state))
				bot.mu.Unlock()
			}
		}

		// --- smart alarm ---
		if ec := b.GetEntityChanged(); ec != nil {
			id := ec.GetEntityId()
			alarm, watched := bot.alarms[id]
			if !watched {
				return
			}
			if p := ec.GetPayload(); p != nil && p.Value != nil {
				if p.GetValue() {
					text := fmt.Sprintf("[ALARM TRIGGERED] %s (%d): %s", alarm.name, id, alarm.msg)
					log.Println(text)
					bot.rpc.BotSay(text)
					if alarm.callback != nil {
						go alarm.callback() // не блокируем обработчик
					}
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
	if number > 2 || number <= 0 {
		return errors.New("такую кнокпу нельзя установить")
	}

	sw := smartSwitch{id: switchId, name: switchName, state: false}

	if number == 1 {
		bot.bt1switch = &sw
	} else {
		bot.bt2switch = &sw
	}

	return nil
}

func (bot *RustPlusBot) SetAlarm(alarmId uint32, alarmName string, alarmMsg string, triggerFunc func()) {
	sw := smartAlarm{name: alarmName, msg: alarmMsg, callback: triggerFunc}
	bot.alarms[alarmId] = sw
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

	// подключаем WS
	if err := bot.rpc.Connect(ctx); err != nil {
		cancel()
		return err
	}

	// BM scan
	if bot.bm != nil {
		notify := func(text string) {
			_ = bot.rpc.SendTeamMessage(text, nil)
			log.Println(text)
		}
		if err := bot.bm.StartScan(1*time.Minute, notify); err != nil {
			log.Println("bm scan:", err)
		}
	}

	// mediahook
	if bot.mediaHook != nil {
		if err := bot.mediaHook.Start(); err != nil {
			log.Println("mediahook:", err)
		}
	}

	// начальная синхронизация состояния свитчей (читаем value, не переключаем!)
	initSwitch := func(sw *smartSwitch) {
		if sw == nil {
			return
		}
		id := sw.id
		_ = bot.rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
			if info := m.GetResponse().GetEntityInfo(); info != nil {
				val := false
				if p := info.GetPayload(); p != nil && p.Value != nil {
					val = p.GetValue()
				}
				bot.mu.Lock()
				sw.state = val
				bot.mu.Unlock()
				log.Printf("Init %s (%d): value=%v\n", sw.name, id, val)
			}
			return true
		})
	}
	initSwitch(bot.bt1switch)
	initSwitch(bot.bt2switch)

	// init alarms (лог)
	for id, alarm := range bot.alarms {
		_ = bot.rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
			info := m.GetResponse().GetEntityInfo()
			if info != nil {
				log.Printf("Init %s (%d): type=%v, value=%v\n",
					alarm.name, id, info.GetType(), info.GetPayload().GetValue())
			}
			return true
		})
	}

	// фоновой «сторож»: ждём стоп и прибираем ресурсы
	bot.wg.Add(1)
	go func() {
		defer bot.wg.Done()
		<-bot.stopCh
		// порядок остановки
		if bot.mediaHook != nil {
			bot.mediaHook.Close()
		}
		if bot.bm != nil {
			bot.bm.Stop()
		}
		cancel()             // прервать readLoop
		bot.rpc.Disconnect() // закрыть сокет
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

func (bot *RustPlusBot) turnSwitch(number int) string {
	var sw *smartSwitch
	switch number {
	case 1:
		sw = bot.bt1switch
	case 2:
		sw = bot.bt2switch
	default:
		return "Неверные данные"
	}
	if sw == nil {
		return "Кнопка не инициализирована!"
	}

	sw.Lock()
	defer sw.Unlock()

	if sw.state {
		_ = bot.rpc.TurnSmartSwitchOff(sw.id, nil)
		sw.state = false
		return fmt.Sprintf("%s : off", sw.name)
	}
	_ = bot.rpc.TurnSmartSwitchOn(sw.id, nil)
	sw.state = true
	return fmt.Sprintf("%s : on", sw.name)
}
