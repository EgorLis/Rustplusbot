package bot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
)

// сплит с поддержкой кавычек: msg="дом рейдят"
var reArg = regexp.MustCompile(`"([^"]*)"|(\S+)`)

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
