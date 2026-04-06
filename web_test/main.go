package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/EgorLis/Rustplusbot/internal/bot/mapw"
	"github.com/EgorLis/Rustplusbot/internal/rustplus"
	"github.com/EgorLis/Rustplusbot/internal/tools"
	"github.com/EgorLis/Rustplusbot/internal/web"
)

func main() {
	var rpcfg rustplus.Config

	tools.MustRead("conf/rpconfig.json", &rpcfg)

	// воспроизведение звука при смерти персонажа

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rpc := rustplus.NewClient(&rpcfg, rustplus.Events{
		OnMessage: func(am *rustplus.AppMessage) {
			log.Println(am)
		},
		OnError: func(err error) {
			log.Println(err)
		},
	})

	rpc.Run()

	rpc.GetMapMarkers(context.Background(), func(am *rustplus.AppMessage) bool {
		if am.GetResponse() != nil && am.GetResponse().GetMapMarkers() != nil {
			log.Println(am.GetResponse().GetMapMarkers())
		}

		return true
	})

	rpc.GetInfo(ctx, func(am *rustplus.AppMessage) bool {
		log.Println("[info]")
		if am.GetResponse() != nil && am.GetResponse().GetInfo() != nil {
			log.Println(am.GetResponse().GetInfo())

			return true
		}

		return false
	})

	rpc.GetMap(ctx, func(am *rustplus.AppMessage) bool {
		if am.GetResponse() != nil && am.GetResponse().GetMap() != nil {
			log.Println("map width", *am.GetResponse().GetMap().Width)
			log.Println("map height", *am.GetResponse().GetMap().Height)

			log.Println("", am.GetResponse().GetMap().Monuments)

			file, err := os.Create("map.png")

			if err != nil {
				log.Println(err)
			}

			defer file.Close()

			file.Write(am.GetResponse().GetMap().JpgImage)

			return true
		}

		return false
	})

	rpc.GetTeamInfo(ctx, func(am *rustplus.AppMessage) bool {
		if am.GetResponse() != nil && am.GetResponse().GetTeamInfo() != nil {
			log.Println(am.GetResponse().GetTeamInfo())

			return true
		}

		return false
	})

	mapWatcher := mapw.NewMapWatcher(rpc)
	mapWatcher.Init(ctx)

	go web.StartServer(rpc, mapWatcher, 8080)

	<-ctx.Done()
	rpc.Stop()
}
