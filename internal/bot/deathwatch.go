package bot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type playerDeath struct {
	steamID       uint64
	muState       sync.Mutex
	lastOnline    *bool
	lastAlive     *bool
	lastDeathTime uint32
	lastLogoutAt  time.Time
	callback      func()
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
		//log.Printf("[death] init: online: %t, alive: %t, death time: %d", online, alive, dt)
		return
	}

	//log.Printf("[death] prev: online: %t, alive: %t, death time: %d", *pd.lastOnline, *pd.lastAlive, pd.lastDeathTime)
	//log.Printf("[death] cur: online: %t, alive: %t, death time: %d", online, alive, dt)

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
