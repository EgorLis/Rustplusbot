package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type Settings struct {
	ServerID       string `json:"server_id"`
	PlayerID       string `json:"player_id"`
	PlayerToken    string `json:"player_token"`
	HasPlayerToken bool   `json:"has_player_token"`
	AuthTokenFile  string `json:"auth_token_file"`
	BMServer       string `json:"bm_server"`
	BMToken        string `json:"bm_token"`
	HasBMToken     bool   `json:"has_bm_token"`
}

type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message,omitempty"`
	Sound   string `json:"sound,omitempty"`
}

type Player struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type BotView struct {
	BT1      *Device          `json:"bt1"`
	BT2      *Device          `json:"bt2"`
	Alarms   []Device         `json:"alarms"`
	Players  []Player         `json:"players"`
	Muted    bool             `json:"muted"`
	Death    *bot.DeathConfig `json:"death"`
	MapWatch []string         `json:"map_watch"`
}

type diskConfig struct {
	Rust rpclient.RustPlusConfig
	BM   bmapi.BMConf
	Bot  bot.BotConfig
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("invalid JSON in %s", filepath.Base(path))
	}
	return nil
}

func loadConfig(dir string) (diskConfig, error) {
	var cfg diskConfig
	for name, target := range map[string]any{"rpconfig.json": &cfg.Rust, "bmconfig.json": &cfg.BM, "botconfig.json": &cfg.Bot} {
		if err := readJSON(filepath.Join(dir, name), target); err != nil {
			return cfg, err
		}
	}

	if cfg.Bot.Alarms == nil {
		cfg.Bot.Alarms = map[uint64]bot.AlarmConf{}
	}
	if cfg.Bot.Players == nil {
		cfg.Bot.Players = []bot.PlayerConf{}
	}
	return cfg, nil
}

func (cfg diskConfig) settings() Settings {
	playerID := ""
	if cfg.Rust.PlayerID != 0 {
		playerID = strconv.FormatUint(cfg.Rust.PlayerID, 10)
	}

	return Settings{ServerID: cfg.Rust.ServerID, PlayerID: playerID, HasPlayerToken: cfg.Rust.PlayerID != 0,
		AuthTokenFile: cfg.Rust.AuthTokenFile, BMServer: cfg.BM.Server, HasBMToken: cfg.BM.Token != ""}
}

func (cfg diskConfig) view(online map[string]string) BotView {
	device := func(sw *bot.SwitchConf) *Device {
		if sw == nil {
			return nil
		}
		return &Device{ID: strconv.FormatUint(sw.ID, 10), Name: sw.Name}
	}

	view := BotView{BT1: device(cfg.Bot.BT1), BT2: device(cfg.Bot.BT2), Alarms: []Device{}, Players: []Player{}, Muted: cfg.Bot.AlarmsMuted, Death: cfg.Bot.Death}
	view.MapWatch = append([]string{}, cfg.Bot.MapWatch...)
	for _, alarm := range cfg.Bot.Alarms {
		view.Alarms = append(view.Alarms, Device{ID: strconv.FormatUint(alarm.ID, 10), Name: alarm.Name, Message: alarm.Msg, Sound: alarm.Sound})
	}
	for _, player := range cfg.Bot.Players {
		_, present := online[player.ID]
		view.Players = append(view.Players, Player{ID: player.ID, Name: player.Name, Online: present})
	}
	sort.Slice(view.Alarms, func(i, j int) bool { return view.Alarms[i].ID < view.Alarms[j].ID })
	return view
}

func applySettings(cfg diskConfig, input Settings) (diskConfig, error) {
	input.ServerID = strings.TrimSpace(input.ServerID)
	if !uuidPattern.MatchString(input.ServerID) {
		return cfg, errors.New("укажите UUID сервера из уведомления Rust+")
	}
	id, err := positiveID(input.PlayerID)
	if err != nil {
		return cfg, errors.New("SteamID должен быть положительным целым числом")
	}

	if input.PlayerToken != "" {
		token, err := strconv.ParseInt(strings.TrimSpace(input.PlayerToken), 10, 32)
		if err != nil {
			return cfg, errors.New("playerToken должен быть числом int32")
		}
		cfg.Rust.PlayerToken = int32(token)
	} else if cfg.Rust.PlayerID == 0 || cfg.Rust.PlayerID != id || cfg.Rust.ServerID != input.ServerID {
		return cfg, errors.New("при смене сервера или SteamID введите playerToken из нового уведомления")
	}

	cfg.Rust.ServerID = input.ServerID
	cfg.Rust.PlayerID = id
	if input.AuthTokenFile != cfg.Rust.AuthTokenFile {
		cfg.Rust.AuthToken = ""
	}
	cfg.Rust.AuthTokenFile = strings.TrimSpace(input.AuthTokenFile)
	cfg.BM.Server = strings.TrimSpace(input.BMServer)
	if cfg.BM.Server != "" {
		if _, err := positiveID(cfg.BM.Server); err != nil {
			return cfg, errors.New("BattleMetrics ID должен быть числом")
		}
	}
	if input.BMToken != "" {
		cfg.BM.Token = strings.TrimSpace(input.BMToken)
	}
	if cfg.BM.Server != "" && cfg.BM.Token == "" {
		return cfg, errors.New("введите API-токен BattleMetrics")
	}
	return cfg, nil
}

func positiveID(value string) (uint64, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("ID должен быть положительным целым числом")
	}
	return id, nil
}

func validateBot(cfg bot.BotConfig) error {
	for _, id := range cfg.MapWatch {
		if _, err := positiveID(id); err != nil {
			return errors.New("некорректный SteamID наблюдаемого игрока карты")
		}
	}
	for _, sw := range []*bot.SwitchConf{cfg.BT1, cfg.BT2} {
		if sw != nil && (sw.ID == 0 || strings.TrimSpace(sw.Name) == "") {
			return errors.New("укажите ID и имя переключателя")
		}
	}
	for id, alarm := range cfg.Alarms {
		if id == 0 || alarm.ID != id || strings.TrimSpace(alarm.Name) == "" {
			return errors.New("укажите ID и имя аларма")
		}
		if err := validateSound(alarm.Sound); err != nil {
			return err
		}
	}
	for _, player := range cfg.Players {
		if _, err := positiveID(player.ID); err != nil {
			return errors.New("некорректный ID игрока BattleMetrics")
		}
	}
	if death := cfg.Death; death != nil {
		if death.SteamID != "" {
			if _, err := positiveID(death.SteamID); err != nil {
				return err
			}
		}
		if death.Enabled && death.SteamID == "" {
			return errors.New("для death-watch нужен SteamID")
		}
		if death.Interval < 1 || death.Interval > 3600 {
			return errors.New("интервал death-watch: от 1 до 3600 секунд")
		}
		return validateSound(death.Sound)
	}
	return nil
}

func validateSound(value string) error {
	if strings.ContainsAny(value, `/\:`) || value == "." || value == ".." {
		return errors.New("выберите имя файла из папки sounds")
	}
	return nil
}

// Writes are atomic per file. A .bak file preserves the previous settings.
func writeJSON(dir, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	old, err := os.ReadFile(path)
	if err == nil {
		if err := os.WriteFile(path+".bak", old, 0600); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	f, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
