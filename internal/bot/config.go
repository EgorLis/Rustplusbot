package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
)

type SwitchConf struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type AlarmConf struct {
	ID    uint64 `json:"id"`
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
	Alarms map[uint64]AlarmConf `json:"alarms"`
	// Список отслеживаемых игроков для BM:
	Players     []PlayerConf `json:"players"`
	Death       *DeathConfig `json:"death,omitempty"`
	AlarmsMuted bool         `json:"alarms_muted,omitempty"`
	MapWatch    []string     `json:"map_watch,omitempty"`
}

type DeathConfig struct {
	SteamID  string `json:"steam_id"`
	Sound    string `json:"sound"`
	Interval int    `json:"interval_sec"`
	Enabled  bool   `json:"enabled"`
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

	bot.amMu.Lock()
	bot.amIsMuted = bot.cfg.data.AlarmsMuted
	bot.amMu.Unlock()

	if death := bot.cfg.data.Death; death != nil && death.SteamID != "" {
		id, err := strconv.ParseUint(death.SteamID, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid death-watch SteamID")
		}
		bot.SetCheckPlayerDeath(id, &death.Sound)
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
			data, marshalErr := json.MarshalIndent(cs.data, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}

			return os.WriteFile(f, data, 0644) // создаём пустой
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

	return atomicConfigWrite(cs.path, b)
}

func atomicConfigWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".botconfig-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func newConfigStore(path string) *configStore {
	return &configStore{
		path: path,
		data: BotConfig{Alarms: map[uint64]AlarmConf{}},
	}
}
