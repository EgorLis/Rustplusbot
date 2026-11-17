package webui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type Runtime interface {
	StartContext(context.Context) error
	Stop()
	UpdateConfig(func(*bot.BotConfig) error) error
	RuntimeState() bot.RuntimeState
	SetSwitchValue(int, bool) error
	DeviceInfo(uint64) (*rpclient.AppEntityInfo, error)
	TeamInfo() (*rpclient.AppTeamInfo, error)
	ServerPlayers() map[string]string
	RefreshServerPlayers() (map[string]string, error)
}

type Factory func(string) (Runtime, error)

type Controller struct {
	generation    uint64
	mapData       mapDataStore
	notifications *notificationHub
	opMu          sync.Mutex
	mu            sync.Mutex
	ctx           context.Context
	conf          string
	factory       Factory
	current       Runtime
	cancel        context.CancelFunc
	done          chan struct{}
	phase         string
	lastError     string
	players       map[string]string
	playersLoaded bool
	logs          logBuffer
}

func New(ctx context.Context, conf string, factory Factory) *Controller {
	return &Controller{notifications: newNotificationHub(ctx, conf), ctx: ctx, conf: conf, factory: factory, phase: "stopped", players: map[string]string{}}
}

func (c *Controller) Start() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.start()
}

func (c *Controller) start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.done != nil {
		return errors.New("бот уже запущен или подключается")
	}
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}

	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	c.cancel, c.done = cancel, done
	c.generation++
	c.phase, c.lastError = "starting", ""

	go func() {
		var runError error
		defer func() {
			cancel()
			c.mu.Lock()
			c.current, c.cancel, c.done = nil, nil, nil
			if runError != nil && !errors.Is(runError, context.Canceled) && !errors.Is(runError, context.DeadlineExceeded) {
				c.phase, c.lastError = "error", runError.Error()
				log.Println("Web UI: bot stopped:", runError)
			} else {
				c.phase = "stopped"
			}
			close(done)
			c.mu.Unlock()
		}()

		runtime, err := c.factory(c.conf)
		if err != nil {
			runError = err
			return
		}
		defer runtime.Stop()

		c.mu.Lock()
		c.current = runtime
		c.mu.Unlock()

		if err := runtime.StartContext(ctx); err != nil {
			runError = err
			return
		}

		c.mu.Lock()
		if ctx.Err() == nil {
			c.phase = "running"
		}
		c.mu.Unlock()

		<-ctx.Done()
	}()

	return nil
}

func (c *Controller) Stop() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.stop()
}

func (c *Controller) stop() error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	if done != nil {
		c.phase = "stopping"
	}
	c.mu.Unlock()
	if done == nil {
		return nil
	}

	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(15 * time.Second):
		return errors.New("остановка ещё выполняется; дождитесь завершения")
	}
}

func (c *Controller) SaveSettings(input Settings) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	previous, err := loadConfig(c.conf)
	if err != nil {
		return err
	}
	cfg, err := applySettings(previous, input)
	if err != nil {
		return err
	}

	c.mu.Lock()
	active := c.done != nil
	c.mu.Unlock()
	changed := !reflect.DeepEqual(previous.Rust, cfg.Rust) || !reflect.DeepEqual(previous.BM, cfg.BM)
	if active && changed {
		if err := c.stop(); err != nil {
			return err
		}
	}

	if err := writeJSON(c.conf, "rpconfig.json", cfg.Rust); err != nil {
		return err
	}
	if err := writeJSON(c.conf, "bmconfig.json", cfg.BM); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(c.conf, "botconfig.json")); errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(c.conf, "botconfig.json", cfg.Bot); err != nil {
			return err
		}
	}

	if changed {
		c.mu.Lock()
		c.players = map[string]string{}
		c.playersLoaded = false
		c.mu.Unlock()
	}
	if active && changed {
		return c.start()
	}
	return nil
}

type Action struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Message  string `json:"message"`
	Sound    string `json:"sound"`
	Slot     int    `json:"slot"`
	Value    bool   `json:"value"`
	Interval int    `json:"interval"`
	Enabled  bool   `json:"enabled"`
}

func (c *Controller) Action(input Action) (any, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	c.mu.Lock()
	runtime, phase := c.current, c.phase
	c.mu.Unlock()

	if input.Action == "switch_value" || input.Action == "device_info" || input.Action == "team" {
		if runtime == nil || phase != "running" {
			return nil, errors.New("сначала подключите бота")
		}
		switch input.Action {
		case "switch_value":
			return nil, runtime.SetSwitchValue(input.Slot, input.Value)
		case "team":
			team, err := runtime.TeamInfo()
			if err != nil {
				return nil, err
			}
			members := []map[string]any{}
			for _, member := range team.GetMembers() {
				members = append(members, map[string]any{"id": strconv.FormatUint(member.GetSteamId(), 10), "name": member.GetName(), "online": member.GetIsOnline(), "alive": member.GetIsAlive()})
			}
			return members, nil
		case "device_info":
			id, err := positiveID(input.ID)
			if err != nil {
				return nil, err
			}
			info, err := runtime.DeviceInfo(id)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": input.ID, "type": info.GetType().String(), "value": info.GetPayload().GetValue(), "capacity": info.GetPayload().GetCapacity(), "protected": info.GetPayload().GetHasProtection()}, nil
		}
	}

	if phase == "starting" || phase == "stopping" {
		return nil, errors.New("дождитесь завершения подключения или остановки")
	}
	if runtime != nil {
		return nil, runtime.UpdateConfig(func(cfg *bot.BotConfig) error { return mutateAction(input, cfg) })
	}

	cfg, err := loadConfig(c.conf)
	if err != nil {
		return nil, err
	}
	if err := mutateAction(input, &cfg.Bot); err != nil {
		return nil, err
	}
	return nil, writeJSON(c.conf, "botconfig.json", cfg.Bot)
}

func mutateAction(input Action, cfg *bot.BotConfig) error {
	switch input.Action {
	case "track_add":
		if _, err := positiveID(input.ID); err != nil {
			return err
		}
		found := false
		for i := range cfg.Players {
			if cfg.Players[i].ID == input.ID {
				cfg.Players[i].Name = input.Name
				found = true
			}
		}
		if !found {
			cfg.Players = append(cfg.Players, bot.PlayerConf{ID: input.ID, Name: input.Name})
		}
	case "track_delete":
		players := []bot.PlayerConf{}
		for _, player := range cfg.Players {
			if player.ID != input.ID {
				players = append(players, player)
			}
		}
		cfg.Players = players
	case "switch_set":
		if input.Slot < 1 || input.Slot > 2 {
			return errors.New("выберите BT1 или BT2")
		}
		var sw *bot.SwitchConf
		if input.ID != "" {
			id, err := positiveID(input.ID)
			if err != nil {
				return err
			}
			sw = &bot.SwitchConf{ID: id, Name: strings.TrimSpace(input.Name)}
		}
		if input.Slot == 1 {
			cfg.BT1 = sw
		} else {
			cfg.BT2 = sw
		}
	case "alarm_save", "alarm_delete":
		id, err := positiveID(input.ID)
		if err != nil {
			return err
		}
		if input.Action == "alarm_delete" {
			delete(cfg.Alarms, id)
		} else {
			cfg.Alarms[id] = bot.AlarmConf{ID: id, Name: strings.TrimSpace(input.Name), Msg: input.Message, Sound: input.Sound}
		}
	case "alarm_mute":
		cfg.AlarmsMuted = input.Value
	case "death_save":
		cfg.Death = &bot.DeathConfig{SteamID: input.ID, Sound: input.Sound, Interval: input.Interval, Enabled: input.Enabled}
	case "map_watch":
		if _, err := positiveID(input.ID); err != nil {
			return err
		}
		ids := []string{}
		for _, id := range cfg.MapWatch {
			if id != input.ID {
				ids = append(ids, id)
			}
		}
		if input.Enabled {
			ids = append(ids, input.ID)
		}
		cfg.MapWatch = ids
	case "save":
	default:
		return fmt.Errorf("unknown action: %s", input.Action)
	}

	return validateBot(*cfg)
}

func (c *Controller) Players() ([]Player, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	c.mu.Lock()
	runtime, phase := c.current, c.phase
	c.mu.Unlock()
	if runtime == nil || phase != "running" {
		return nil, errors.New("сначала подключите бота")
	}
	players, err := runtime.RefreshServerPlayers()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.players = players
	c.playersLoaded = true
	c.mu.Unlock()
	return playerList(players), nil
}

func playerList(players map[string]string) []Player {
	result := []Player{}
	for id, name := range players {
		result = append(result, Player{ID: id, Name: name, Online: true})
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result
}

func (c *Controller) State() (any, error) {
	cfg, err := loadConfig(c.conf)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	phase, lastError, runtime := c.phase, c.lastError, c.current
	generation := c.generation
	playersLoaded := c.playersLoaded
	players := make(map[string]string, len(c.players))
	for id, name := range c.players {
		players[id] = name
	}
	c.mu.Unlock()

	var state bot.RuntimeState
	if runtime != nil {
		state = runtime.RuntimeState()
		if !playersLoaded {
			players = runtime.ServerPlayers()
		}
	}
	return map[string]any{"phase": phase, "error": lastError, "runtime": state, "settings": cfg.settings(), "bot": cfg.view(players), "players": playerList(players), "logs": c.logs.String(), "notifications": c.notifications.snapshot(), "map": c.mapSnapshot(cfg.Rust.ServerID, state.WipeTime, generation)}, nil
}

type logBuffer struct {
	mu   sync.Mutex
	data string
}

func (b *logBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data += string(data)
	if len(b.data) > 65536 {
		b.data = b.data[len(b.data)-65536:]
	}
	return len(data), nil
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data
}
