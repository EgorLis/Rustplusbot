package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"example.com/volhook/pkg/bmapi"
	"example.com/volhook/pkg/rpbot"
	"example.com/volhook/pkg/rpclient"
)

func main() {
	// Пример чтения из файла config.json
	data, err := os.ReadFile("conf/rpconfig.json")
	if err != nil {
		log.Fatal(err)
	}

	var rpcfg rpclient.RustPlusConfig
	if err := json.Unmarshal(data, &rpcfg); err != nil {
		log.Fatal(err)
	}

	// Пример чтения из файла config.json
	data, err = os.ReadFile("conf/bmconfig.json")
	if err != nil {
		log.Fatal(err)
	}

	var bmcfg bmapi.BMConf
	if err := json.Unmarshal(data, &bmcfg); err != nil {
		log.Fatal(err)
	}

	// воспроизведение звука при смерти персонажа
	sound := "3.mp3"

	bot := rpbot.New()
	bot.SetRustPlusClient(rpcfg)
	bot.SetBattleMetrics(bmcfg)
	bot.SetMediaHook()
	bot.SetCheckPlayerDeath(rpcfg.PlayerID, &sound)

	// подключим конфиг бота и применим его (alarms/switches/players)
	if err := bot.UseConfig("conf/botconfig.json"); err != nil {
		log.Fatal(err)
	}

	if err := bot.Start(); err != nil {
		log.Fatal(err)
	}
	defer bot.Stop()

	fmt.Println("running… press Ctrl+C to stop")
	// держим процесс живым
	select {}
}
