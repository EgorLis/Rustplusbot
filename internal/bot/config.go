package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
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

func newConfigStore(path string) *configStore {
	return &configStore{
		path: path,
		data: BotConfig{Alarms: map[uint32]AlarmConf{}},
	}
}
