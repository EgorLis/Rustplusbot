package bot

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
	"google.golang.org/protobuf/proto"
)

type RuntimeState struct {
	Connected     bool            `json:"connected"`
	Server        string          `json:"server"`
	Switches      map[string]bool `json:"switches"`
	DeathRunning  bool            `json:"death_running"`
	DeathInterval int             `json:"death_interval"`
	WipeTime      uint32          `json:"wipe_time"`
	ChatError     string          `json:"chat_error,omitempty"`
}

func (bot *RustPlusBot) RuntimeState() RuntimeState {
	bot.commandMu.Lock()
	defer bot.commandMu.Unlock()

	state := RuntimeState{Connected: bot.rpc.IsConnected(), Server: bot.rpc.ServerInfo().GetName(), Switches: map[string]bool{}}
	state.WipeTime = bot.rpc.ServerInfo().GetWipeTime()
	state.ChatError = bot.rpc.TeamChatError()
	for key, sw := range map[string]*smartSwitch{"1": bot.bt1switch, "2": bot.bt2switch} {
		if sw != nil {
			sw.Lock()
			if sw.known {
				state.Switches[key] = sw.state
			}
			sw.Unlock()
		}
	}

	bot.dwMu.Lock()
	state.DeathRunning = bot.dwRunning
	state.DeathInterval = int(bot.dwEvery / time.Second)
	bot.dwMu.Unlock()

	return state
}

// ApplyConfig changes runtime bindings and tracked players without reconnecting Rust+.
// Its caller persists the validated config before publishing it here.
func (bot *RustPlusBot) ApplyConfig(cfg BotConfig) error {
	bot.commandMu.Lock()
	defer bot.commandMu.Unlock()

	return bot.applyConfigLocked(cfg)
}

// UpdateConfig serializes web edits with chat commands, including their disk write.
func (bot *RustPlusBot) UpdateConfig(edit func(*BotConfig) error) error {
	bot.commandMu.Lock()
	defer bot.commandMu.Unlock()

	if bot.cfg == nil {
		return errors.New("bot config is not enabled")
	}
	bot.cfg.mu.Lock()
	before, err := json.MarshalIndent(bot.cfg.data, "", "  ")
	bot.cfg.mu.Unlock()
	if err != nil {
		return err
	}

	var cfg BotConfig
	if err := json.Unmarshal(before, &cfg); err != nil {
		return err
	}
	if cfg.Alarms == nil {
		cfg.Alarms = map[uint64]AlarmConf{}
	}
	if err := edit(&cfg); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(bot.cfg.path+".bak", before, 0600); err != nil {
		return err
	}
	if err := atomicConfigWrite(bot.cfg.path, data); err != nil {
		return err
	}

	return bot.applyConfigLocked(cfg)
}

func (bot *RustPlusBot) applyConfigLocked(cfg BotConfig) error {
	if bot.cfg == nil {
		return errors.New("bot config is not enabled")
	}

	// Take a private copy so an HTTP request cannot retain mutable runtime maps.
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	bot.cfg.mu.Lock()
	previousDeath := bot.cfg.data.Death
	bot.cfg.data = cfg
	bot.cfg.mu.Unlock()

	bind := func(current *smartSwitch, conf *SwitchConf) *smartSwitch {
		if conf == nil {
			return nil
		}
		if current != nil && current.id == conf.ID && current.name == conf.Name {
			return current
		}
		return &smartSwitch{id: conf.ID, name: conf.Name}
	}
	bot.bt1switch = bind(bot.bt1switch, cfg.BT1)
	bot.bt2switch = bind(bot.bt2switch, cfg.BT2)
	bot.alarms = make(map[uint64]smartAlarm, len(cfg.Alarms))
	for id, alarm := range cfg.Alarms {
		bot.alarms[id] = smartAlarm{name: alarm.Name, msg: alarm.Msg, callback: bot.callbackForSound(alarm.Sound)}
		bot.initAlarmByID(id)
	}

	bot.initSwitch(bot.bt1switch)
	bot.initSwitch(bot.bt2switch)

	if bot.bm != nil {
		players := make([]bmapi.Player, 0, len(cfg.Players))
		for _, player := range cfg.Players {
			players = append(players, bmapi.Player{ID: player.ID, Name: player.Name})
		}
		bot.bm.SetTrackedPlayers(players)
	}

	bot.amMu.Lock()
	bot.amIsMuted = cfg.AlarmsMuted
	bot.amMu.Unlock()

	if reflect.DeepEqual(previousDeath, cfg.Death) {
		return nil
	}

	bot.StopDeathWatch()
	if death := cfg.Death; death != nil && death.SteamID != "" {
		id, err := strconv.ParseUint(death.SteamID, 10, 64)
		if err != nil {
			return errors.New("invalid death-watch SteamID")
		}
		bot.SetCheckPlayerDeath(id, &death.Sound)
		if death.Enabled {
			return bot.StartDeathWatch(time.Duration(death.Interval) * time.Second)
		}
	}

	return nil
}

func (bot *RustPlusBot) DeviceInfo(id uint64) (*rpclient.AppEntityInfo, error) {
	response, err := bot.rpc.SendRequestAsync(&rpclient.AppRequest{EntityId: proto.Uint64(id), GetEntityInfo: &rpclient.AppEmpty{}}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if response.EntityInfo == nil {
		return nil, errors.New("device returned no information")
	}
	return response.EntityInfo, nil
}

func (bot *RustPlusBot) SetSwitchValue(slot int, value bool) error {
	bot.commandMu.Lock()
	sw := bot.bt1switch
	if slot == 2 {
		sw = bot.bt2switch
	}
	bot.commandMu.Unlock()
	if slot < 1 || slot > 2 || sw == nil {
		return errors.New("switch is not configured")
	}

	before, err := bot.DeviceInfo(sw.id)
	if err != nil {
		return err
	}
	if before.GetType() != rpclient.AppEntityType_Switch {
		return errors.New("устройство не является Smart Switch")
	}

	if err := bot.rpc.SetEntityValue(sw.id, value, nil); err != nil {
		return err
	}
	info, err := bot.DeviceInfo(sw.id)
	if err != nil {
		return err
	}
	if info.GetPayload().GetValue() != value {
		return errors.New("server did not confirm the requested switch state")
	}

	sw.Lock()
	sw.state = value
	sw.known = true
	sw.Unlock()

	return nil
}

func (bot *RustPlusBot) TeamInfo() (*rpclient.AppTeamInfo, error) {
	response, err := bot.rpc.SendRequestAsync(&rpclient.AppRequest{GetTeamInfo: &rpclient.AppEmpty{}}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return response.TeamInfo, nil
}

func (bot *RustPlusBot) MapData() (*rpclient.AppMap, uint32, error) {
	response, err := bot.rpc.SendRequestAsync(&rpclient.AppRequest{GetMap: &rpclient.AppEmpty{}}, 20*time.Second)
	if err != nil {
		return nil, 0, err
	}
	if response.Map == nil {
		return nil, 0, errors.New("сервер не вернул карту")
	}
	return response.Map, bot.rpc.ServerInfo().GetMapSize(), nil
}

func (bot *RustPlusBot) ServerPlayers() map[string]string {
	if bot.bm == nil {
		return map[string]string{}
	}
	return bot.bm.Players()
}

func (bot *RustPlusBot) RefreshServerPlayers() (map[string]string, error) {
	if bot.bm == nil {
		return nil, errors.New("BattleMetrics не настроен")
	}
	return bot.bm.OnlinePlayers()
}
