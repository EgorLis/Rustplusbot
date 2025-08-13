package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

func mustRead[T any](path string, out *T) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		log.Fatal(err)
	}
}

func main() {
	var rpcfg rpclient.RustPlusConfig
	var bmcfg bmapi.BMConf

	mustRead("conf/rpconfig.json", &rpcfg)
	mustRead("conf/bmconfig.json", &bmcfg)

	// воспроизведение звука при смерти персонажа

	b := bot.New()
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
	defer b.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("running… press Ctrl+C to stop")

	<-ctx.Done()
}
