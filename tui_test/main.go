package main

import (
	"log"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rustplus"
	"github.com/EgorLis/Rustplusbot/internal/tools"
	"github.com/EgorLis/Rustplusbot/internal/tui"
)

func main() {
	var rpcfg rustplus.Config
	var bmcfg bmapi.BMConf

	tools.MustRead("conf/rpconfig.json", &rpcfg)
	tools.MustRead("conf/bmconfig.json", &bmcfg)

	// воспроизведение звука при смерти персонажа

	b := bot.New()

	b.UseCircularBufferLogs(1000)

	b.SetRustPlusClient(rpcfg)
	b.SetBattleMetrics(bmcfg)
	b.SetMediaHook()

	// опционально:
	sound := "3.mp3"
	b.SetCheckPlayerDeath(rpcfg.PlayerID, &sound)

	// подключим конфиг бота и применим его (alarms/switches/players)
	if err := b.UseConfig("conf/botconfig.json"); err != nil {
		log.Fatal(err)
	}

	if err := b.Start(); err != nil {
		log.Fatal(err)
	}

	// log.Println("running… press Ctrl+C to stop")

	log.Fatal(tui.NewApp(b).Run())

	b.Stop()
}
