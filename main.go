package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"example.com/volhook/pkg/mediahook"
	"example.com/volhook/pkg/rpclient"
)

type RustPlusConfig struct {
	Server      string `json:"server"`
	Port        int    `json:"port"`
	PlayerID    uint64 `json:"player_id"`
	PlayerToken int32  `json:"player_token"`
	UseProxy    bool   `json:"use_proxy"`
}

func main() {
	// Пример чтения из файла config.json
	data, err := os.ReadFile("conf/config.json")
	if err != nil {
		log.Fatal(err)
	}

	var cfg RustPlusConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatal(err)
	}
	// ID твоей smart‑сигналки (можно несколько)
	alarmIDs := map[uint32]string{
		433470: "Main Alarm",
	}

	ctx := context.Background()
	rpc := rpclient.New(cfg.Server, cfg.Port, cfg.PlayerID, cfg.PlayerToken, cfg.UseProxy)

	// события (необязательно)
	rpc.OnConnecting = func() { fmt.Println("connecting...") }
	rpc.OnConnected = func() { fmt.Println("connected") }
	rpc.OnError = func(err error) { fmt.Println("err:", err) }
	// главный обработчик всех входящих сообщений
	rpc.OnMessage = func(msg *rpclient.AppMessage) {
		b := msg.GetBroadcast()
		if b == nil {
			return
		}

		ec := b.GetEntityChanged()
		if ec == nil {
			return
		}

		id := ec.GetEntityId()
		name, watched := alarmIDs[id]
		if !watched {
			return
		} // не наши устройства — игнор

		payload := ec.GetPayload()
		if payload == nil || payload.Value == nil {
			return
		}

		triggered := payload.GetValue()
		if triggered {
			// СЕРЕНА! Реакция на alarm ON
			fmt.Printf("[ALARM ON] %s (%d)\n", name, id)
			beep()

		} else {
			fmt.Printf("[ALARM OFF] %s (%d)\n", name, id)
		}
	}

	if err := rpc.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer rpc.Disconnect()

	// включить свитч
	_ = rpc.TurnSmartSwitchOn(470670, nil)

	_ = rpc.SendTeamMessage("Hello", nil)

	// (опц.) можно запросить тип устройства, чтобы убедиться, что это именно Alarm
	for id, label := range alarmIDs {
		_ = rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
			info := m.GetResponse().GetEntityInfo()
			if info != nil {
				fmt.Printf("Init %s (%d): type=%v, value=%v\n",
					label, id, info.GetType(), info.GetPayload().GetValue())
			}
			return true
		})
	}

	h, err := mediahook.New(
		func() {
			_ = rpc.TurnSmartSwitchOn(470670, nil)
			fmt.Println("UP pressed") /* тут: действие бота */
		},
		func() {
			_ = rpc.TurnSmartSwitchOff(470670, nil)
			fmt.Println("DOWN pressed") /* тут: действие бота */
		},
		mediahook.WithMute(func() { fmt.Println("MUTE pressed") }),
	)
	if err != nil {
		panic(err)
	}
	defer h.Close()

	if err := h.Start(); err != nil {
		panic(err)
	}

	fmt.Println("running… press Ctrl+C in terminal to stop")

	select {}
}

func beep() { fmt.Print("\a") } // тут можешь вставить проигрывание WAV/MP3
