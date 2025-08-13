package rpbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/volhook/pkg/bmapi"
	"example.com/volhook/pkg/mediahook"
	"example.com/volhook/pkg/rpclient"
)

type SwitchConf struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
}

type AlarmConf struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name"`
	Msg   string `json:"msg"`
	Sound string `json:"sound"` // "none" или "1.mp3"
}

type PlayerConf struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type BotConfig struct {
	BT1    *SwitchConf          `json:"bt1,omitempty"`
	BT2    *SwitchConf          `json:"bt2,omitempty"`
	Alarms map[uint32]AlarmConf `json:"alarms"`
	// Список отслеживаемых игроков для BM:
	Players []PlayerConf `json:"players"`
}

type configStore struct {
	mu   sync.Mutex
	path string
	data BotConfig
}

type playerDeath struct {
	steamID       uint64
	muState       sync.Mutex
	lastOnline    *bool
	lastAlive     *bool
	lastDeathTime uint32
	lastLogoutAt  time.Time
	callback      func()
}

func newConfigStore(path string) *configStore {
	return &configStore{
		path: path,
		data: BotConfig{Alarms: map[uint32]AlarmConf{}},
	}
}

func (cs *configStore) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	f := cs.path
	_ = os.MkdirAll(filepath.Dir(f), 0755)
	b, err := os.ReadFile(f)
	if err != nil {
		if os.IsNotExist(err) {
			return cs.Save() // создаём пустой
		}
		return err
	}
	return json.Unmarshal(b, &cs.data)
}

func (cs *configStore) Save() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	b, err := json.MarshalIndent(&cs.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cs.path, b, 0644)
}

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

func (bot *RustPlusBot) SetCheckPlayerDeath(steamID uint64, sound *string) {
	death := playerDeath{steamID: steamID}
	bot.checkPlayerDeath = &death
	if sound != nil {
		bot.checkPlayerDeath.callback = bot.callbackForSound(*sound)
	}
}

func (bot *RustPlusBot) UseConfig(path string) error {
	bot.cfg = newConfigStore(path)
	if err := bot.cfg.Load(); err != nil {
		return err
	}
	// Применим конфиг в рантайме:
	// BT1/BT2
	if bot.cfg.data.BT1 != nil {
		_ = bot.SetSwitch(1, bot.cfg.data.BT1.ID, bot.cfg.data.BT1.Name)
	}
	if bot.cfg.data.BT2 != nil {
		_ = bot.SetSwitch(2, bot.cfg.data.BT2.ID, bot.cfg.data.BT2.Name)
	}
	// Alarms
	for _, a := range bot.cfg.data.Alarms {
		a := a
		bot.SetAlarm(a.ID, a.Name, a.Msg, bot.callbackForSound(a.Sound))
	}
	// Players (BM)
	if bot.bm != nil && len(bot.cfg.data.Players) > 0 {
		var ps []bmapi.Player
		for _, p := range bot.cfg.data.Players {
			ps = append(ps, bmapi.Player{ID: p.ID, Name: p.Name})
		}
		bot.bm.AddPlayer(ps...)
	}
	return nil
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

func PlaySoundFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// start — откроет файл через ассоциированную программу
		cmd = exec.Command("cmd", "/C", "start", "", path)
	case "darwin":
		// macOS
		cmd = exec.Command("open", path)
	default:
		// Linux
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func (bot *RustPlusBot) StartDeathWatch(every time.Duration) error {
	bot.dwMu.Lock()
	defer bot.dwMu.Unlock()

	if bot.checkPlayerDeath == nil {
		return fmt.Errorf("death-watch: steamID не задан (вызови SetCheckPlayerDeath)")
	}
	if bot.dwRunning {
		// можно обновить интервал на лету
		bot.dwEvery = every
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	bot.dwCancel = cancel
	bot.dwEvery = every
	bot.dwRunning = true

	go bot.deathPollLoop(ctx) // фоновая горутина
	return nil
}

func (bot *RustPlusBot) StopDeathWatch() {
	bot.dwMu.Lock()
	defer bot.dwMu.Unlock()
	if !bot.dwRunning {
		return
	}
	bot.dwRunning = false
	if bot.dwCancel != nil {
		bot.dwCancel()
		bot.dwCancel = nil
	}
}

func (bot *RustPlusBot) HandleCommand(text string) error {
	fields := splitArgs(text)
	if len(fields) == 0 {
		return nil
	}
	cmd := strings.ToLower(fields[0])

	say := func(s string) { _ = bot.rpc.BotSay(s) }

	switch cmd {

	case "!help":
		say(strings.Join([]string{
			"!help",
			"!bt status",
			"!bt1 set <id> <name>",
			"!bt2 set <id> <name>",
			"!alarm add <id> <name> [msg=\"...\"] [sound=1.mp3|none]",
		}, "\n"))
		say(strings.Join([]string{
			"!alarm del <id>",
			"!alarm list",
			"!track add <steamid> [name]",
			"!track del <steamid>",
			"!track list|info",
		}, "\n"))
		say(strings.Join([]string{
			"!death set <steamid> [sound=1.mp3|none]",
			"!death start [interval_sec]",
			"!death stop",
			"!death status",
			"!save",
		}, "\n"))
		return nil

	// ---------- BT ----------
	case "!bt", "!bt status":
		var s1, s2 string
		if bot.bt1switch != nil {
			s1 = fmt.Sprintf("%s(%d)=%t", bot.bt1switch.name, bot.bt1switch.id, bot.bt1switch.state)
		} else {
			s1 = "unset"
		}
		if bot.bt2switch != nil {
			s2 = fmt.Sprintf("%s(%d)=%t", bot.bt2switch.name, bot.bt2switch.id, bot.bt2switch.state)
		} else {
			s2 = "unset"
		}
		say(fmt.Sprintf("BT1: %s | BT2: %s", s1, s2))
		return nil

	case "!bt1":
		if len(fields) >= 2 && strings.ToLower(fields[1]) == "set" && len(fields) >= 4 {
			id, err := parseUint32(fields[2])
			if err != nil {
				return err
			}
			name := fields[3]
			if err := bot.SetSwitch(1, id, name); err != nil {
				return err
			}
			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				if bot.cfg.data.BT1 == nil {
					bot.cfg.data.BT1 = &SwitchConf{}
				}
				bot.cfg.data.BT1.ID, bot.cfg.data.BT1.Name = id, name
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("BT1 set: %s(%d)", name, id))
			return nil
		}
		return fmt.Errorf("usage: !bt1 set <id> <name>")

	case "!bt2":
		if len(fields) >= 2 && strings.ToLower(fields[1]) == "set" && len(fields) >= 4 {
			id, err := parseUint32(fields[2])
			if err != nil {
				return err
			}
			name := fields[3]
			if err := bot.SetSwitch(2, id, name); err != nil {
				return err
			}
			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				if bot.cfg.data.BT2 == nil {
					bot.cfg.data.BT2 = &SwitchConf{}
				}
				bot.cfg.data.BT2.ID, bot.cfg.data.BT2.Name = id, name
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("BT2 set: %s(%d)", name, id))
			return nil
		}
		return fmt.Errorf("usage: !bt2 set <id> <name>")

	// ---------- ALARMS ----------
	case "!alarm":
		if len(fields) < 2 {
			return fmt.Errorf("usage: !alarm add|del|list")
		}
		sub := strings.ToLower(fields[1])

		switch sub {
		case "list":
			if len(bot.alarms) == 0 {
				say("alarms: (empty)")
				return nil
			}
			var rows []string
			for id, a := range bot.alarms {
				// звук берём из конфига (если он есть)
				sound := "unknown"
				if bot.cfg != nil {
					if ac, ok := bot.cfg.data.Alarms[id]; ok {
						if ac.Sound == "" {
							sound = "none"
						} else {
							sound = ac.Sound
						}
					}
				}
				rows = append(rows, fmt.Sprintf("%s(%d) msg=%q sound=%q", a.name, id, a.msg, sound))
			}
			say("alarms:\n" + strings.Join(rows, "\n"))
			return nil

		case "add":
			if len(fields) < 4 {
				return fmt.Errorf("usage: !alarm add <id> <name> [msg=\"...\"] [sound=1.mp3|none]")
			}
			id, err := parseUint32(fields[2])
			if err != nil {
				return err
			}
			name := fields[3]
			kv := parseKV(fields[4:]) // msg=..., sound=...
			msg := kv["msg"]
			sound := kv["sound"] // "none" | "file.mp3" | ""

			cb := bot.callbackForSound(sound) // откроет файл через ОС, если не "none"/""
			bot.SetAlarm(id, name, msg, cb)

			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				bot.cfg.data.Alarms[id] = AlarmConf{ID: id, Name: name, Msg: msg, Sound: sound}
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("alarm added: %s(%d)", name, id))
			return nil

		case "del":
			if len(fields) < 3 {
				return fmt.Errorf("usage: !alarm del <id>")
			}
			id, err := parseUint32(fields[2])
			if err != nil {
				return err
			}
			delete(bot.alarms, id)
			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				delete(bot.cfg.data.Alarms, id)
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("alarm deleted: %d", id))
			return nil

		default:
			return fmt.Errorf("usage: !alarm add|del|list")
		}

	// ---------- TRACK (BattleMetrics) ----------
	case "!track":
		if bot.bm == nil {
			return fmt.Errorf("BM not connected")
		}
		if len(fields) < 2 {
			return fmt.Errorf("usage: !track add|del|list|info")
		}
		sub := strings.ToLower(fields[1])

		switch sub {
		case "list":
			if bot.cfg == nil || len(bot.cfg.data.Players) == 0 {
				say("tracked: (empty)")
				return nil
			}
			var rows []string
			for _, p := range bot.cfg.data.Players {
				rows = append(rows, fmt.Sprintf("%s (%s)", p.Name, p.ID))
			}
			say("tracked:\n" + strings.Join(rows, "\n"))
			return nil
		case "info":
			if bot.cfg == nil || len(bot.cfg.data.Players) == 0 {
				say("tracked: (empty)")
				return nil
			}
			onlineInfo := bot.bm.IsOnline()
			say(bot.bm.FormatOnlineInfo(onlineInfo))

			return nil

		case "add":
			if len(fields) < 3 {
				return fmt.Errorf("usage: !track add <steamid> [name]")
			}
			steam := fields[2]
			name := ""
			if len(fields) >= 4 {
				name = fields[3]
			}

			bot.bm.AddPlayer(bmapi.Player{ID: steam, Name: name})
			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				// обновим/добавим
				found := false
				for i := range bot.cfg.data.Players {
					if bot.cfg.data.Players[i].ID == steam {
						bot.cfg.data.Players[i].Name = name
						found = true
						break
					}
				}
				if !found {
					bot.cfg.data.Players = append(bot.cfg.data.Players, PlayerConf{ID: steam, Name: name})
				}
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("track added: %s (%s)", name, steam))
			return nil

		case "del":
			if len(fields) < 3 {
				return fmt.Errorf("usage: !track del <steamid>")
			}
			steam := fields[2]

			bot.bm.RemovePlayer(steam)

			if bot.cfg != nil {
				bot.cfg.mu.Lock()
				out := make([]PlayerConf, 0, len(bot.cfg.data.Players))
				for _, p := range bot.cfg.data.Players {
					if p.ID != steam {
						out = append(out, p)
					}
				}
				bot.cfg.data.Players = out
				bot.cfg.mu.Unlock()
				_ = bot.cfg.Save()
			}
			say(fmt.Sprintf("track deleted: %s", steam))
			return nil

		default:
			return fmt.Errorf("usage: !track add|del|list|info")
		}
	case "!death":
		if len(fields) < 2 {
			return fmt.Errorf("usage: !death set|start|stop|status")
		}
		sub := strings.ToLower(fields[1])

		switch sub {
		case "set":
			// !death set <steamid> [sound=1.mp3|none]
			if len(fields) < 3 {
				return fmt.Errorf("usage: !death set <steamid> [sound=1.mp3|none]")
			}
			steamID, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				return fmt.Errorf("bad steamid: %w", err)
			}
			var sound *string
			if len(fields) >= 4 {
				kv := parseKV(fields[3:])
				s := strings.TrimSpace(kv["sound"])
				sound = &s // может быть "none" или имя файла
			}
			bot.SetCheckPlayerDeath(steamID, sound)
			_ = bot.rpc.BotSay(fmt.Sprintf("death-watch target set: %d", steamID))
			return nil

		case "start":
			// !death start [sec]
			sec := 15
			if len(fields) >= 3 {
				if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
					sec = v
				}
			}
			if err := bot.StartDeathWatch(time.Duration(sec) * time.Second); err != nil {
				return err
			}
			_ = bot.rpc.BotSay(fmt.Sprintf("death-watch started (%ds)", sec))
			return nil

		case "stop":
			bot.StopDeathWatch()
			_ = bot.rpc.BotSay("death-watch stopped")
			return nil

		case "status":
			bot.dwMu.Lock()
			running := bot.dwRunning
			ev := bot.dwEvery
			bot.dwMu.Unlock()
			if running {
				_ = bot.rpc.BotSay(fmt.Sprintf("death-watch: running (every %s)", ev))
			} else {
				_ = bot.rpc.BotSay("death-watch: stopped")
			}
			return nil

		default:
			return fmt.Errorf("usage: !death set|start|stop|status")
		}

	// ---------- SAVE ----------
	case "!save":
		if bot.cfg != nil {
			if err := bot.cfg.Save(); err != nil {
				return err
			}
			say("config saved")
			return nil
		}
		return fmt.Errorf("config not enabled")

	default:
		return fmt.Errorf("unknown command. try !help")
	}
}

func parseUint32(s string) (uint32, error) {
	u, err := strconv.ParseUint(s, 10, 32)
	return uint32(u), err
}

// сплит с поддержкой кавычек: msg="дом рейдят"
var reArg = regexp.MustCompile(`"([^"]*)"|(\S+)`)

func splitArgs(s string) []string {
	var out []string
	for _, m := range reArg.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			out = append(out, m[1])
		} else {
			out = append(out, m[2])
		}
	}
	return out
}

func parseKV(args []string) map[string]string {
	res := map[string]string{}
	for _, a := range args {
		kv := strings.SplitN(a, "=", 2)
		if len(kv) == 2 {
			res[strings.ToLower(kv[0])] = kv[1]
		}
	}
	return res
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
		err := bot.rpc.TurnSmartSwitchOff(sw.id, nil)
		if err != nil {
			return fmt.Sprintf("%s: %s", sw.name, err)
		}
		sw.state = false
		return fmt.Sprintf("%s : off", sw.name)
	}
	err := bot.rpc.TurnSmartSwitchOn(sw.id, nil)
	if err != nil {
		return fmt.Sprintf("%s: %s", sw.name, err)
	}
	sw.state = true
	return fmt.Sprintf("%s : on", sw.name)
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

func (bot *RustPlusBot) initSwitch(sw *smartSwitch) {
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
			log.Printf("Switch init %s (%d): value=%v\n", sw.name, id, val)
		}
		return true
	})
}

func (bot *RustPlusBot) initAlarmByID(id uint32) {
	_ = bot.rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
		info := m.GetResponse().GetEntityInfo()
		if info != nil {
			// безопасно достаём имя по id на момент коллбека
			a, ok := bot.alarms[id]
			name := fmt.Sprintf("alarm_%d", id)
			if ok {
				name = a.name
			}
			var val any = nil
			if p := info.GetPayload(); p != nil && p.Value != nil {
				val = p.GetValue()
			}
			log.Printf("Init %s (%d): type=%v, value=%v\n", name, id, info.GetType(), val)
		}
		return true
	})
}

// коллбэк из строки sound
func (bot *RustPlusBot) callbackForSound(sound string) func() {
	s := strings.TrimSpace(sound)
	if s == "" || strings.EqualFold(s, "none") {
		return nil
	}
	path := filepath.Join("sounds", s) // ./sounds/<sound>

	return func() {
		if err := PlaySoundFile(path); err != nil {
			log.Println("sound open error:", err)
		}
	}
}

func (bot *RustPlusBot) checkDeath(ti *rpclient.AppTeamInfo, say func(string)) {

	log.Println("[death] check death...")

	pd := bot.checkPlayerDeath
	if pd == nil {
		return
	}

	// найти себя
	var me *rpclient.AppTeamInfo_Member
	for _, m := range ti.GetMembers() {
		if m.GetSteamId() == pd.steamID {
			me = m
			break
		}
	}
	if me == nil {
		return
	}

	online := me.GetIsOnline()
	alive := me.GetIsAlive()
	dt := me.GetDeathTime()

	pd.muState.Lock()
	defer pd.muState.Unlock()

	// первичная инициализация — просто зафиксировать стартовое состояние
	if pd.lastOnline == nil {
		v1, v2 := online, alive
		pd.lastOnline, pd.lastAlive = &v1, &v2
		pd.lastDeathTime = dt
		if !online {
			pd.lastLogoutAt = time.Now()
		}
		log.Printf("[death] init: online: %t, alive: %t, death time: %d", online, alive, dt)
		return
	}

	log.Printf("[death] prev: online: %t, alive: %t, death time: %d", *pd.lastOnline, *pd.lastAlive, pd.lastDeathTime)
	log.Printf("[death] cur: online: %t, alive: %t, death time: %d", online, alive, dt)

	// отметим момент выхода (может пригодиться в другой логике)
	if *pd.lastOnline && !online {
		pd.lastLogoutAt = time.Now()
	}

	// детект смерти: либо вырос deathTime, либо упал флаг alive
	death := false
	if *pd.lastAlive && !alive {
		death = true
	}

	if death {
		say("Я умер 💀")
		if pd.callback != nil {
			go pd.callback()
		}
		// дальше просто обновим флаги и выйдем
	}

	// финальное обновление флагов (если смерти не было — тоже актуально)
	*pd.lastAlive = alive
	*pd.lastOnline = online
	pd.lastDeathTime = dt
}

// deathPollLoop — живёт всё время, пока не вызовут StopDeathWatch().
func (bot *RustPlusBot) deathPollLoop(ctx context.Context) {
	// один тикер — ваш интервал опроса
	t := time.NewTicker(bot.dwEvery)
	defer t.Stop()

	// чтобы не спамить в разрыв соединения
	var notConnectedBackoff = time.Second
	const maxBackoff = 10 * time.Second

	// чтобы избежать ложного триггера на первом чтении
	initOnce := true

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			// 1) нет соединения? просто «спим» и ждём следующего тика,
			//    можно добавить небольшой прогрессивный backoff чтобы не дёргать CPU
			if !bot.rpc.IsConnected() {
				time.Sleep(notConnectedBackoff)
				if notConnectedBackoff < maxBackoff {
					notConnectedBackoff *= 2
					if notConnectedBackoff > maxBackoff {
						notConnectedBackoff = maxBackoff
					}
				}
				continue
			}
			// есть соединение — сбросим backoff
			notConnectedBackoff = time.Second

			// 2) безопасно дергаем TeamInfo с таймаутом; при реконнекте будет ошибка — не страшно
			resp, err := bot.rpc.SendRequestAsync(&rpclient.AppRequest{
				GetTeamInfo: &rpclient.AppEmpty{},
			}, 8*time.Second)
			if err != nil || resp == nil {
				// сеть/таймаут — молча ждём следующий тик
				continue
			}
			ti := resp.GetTeamInfo()
			if ti == nil {
				continue
			}

			// 3) первый проход — только зафиксировать состояние
			if initOnce {
				bot.checkDeath(ti, func(string) {})
				initOnce = false
				continue
			}

			// 4) обычная обработка: сравнить снапшоты и, если надо, сообщить
			bot.checkDeath(ti, func(msg string) { _ = bot.rpc.BotSay(msg) })
		}
	}
}
