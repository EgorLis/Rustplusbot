package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"example.com/volhook/pkg/bmapi"
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
	data, err := os.ReadFile("conf/rpconfig.json")
	if err != nil {
		log.Fatal(err)
	}

	var rpcfg RustPlusConfig
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

	bm := bmapi.NewClientFromConf(bmcfg)

	// ID твоей smart‑сигналки (можно несколько)
	alarmIDs := map[uint32]string{
		433470: "Main Alarm",
	}

	ctx := context.Background()
	rpc := rpclient.New(rpcfg.Server, rpcfg.Port, rpcfg.PlayerID, rpcfg.PlayerToken, rpcfg.UseProxy)

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

		// --- 1. Обработка сообщений чата ---
		if chat := b.GetTeamMessage(); chat != nil {
			text := strings.TrimSpace(*chat.Message.Message)

			// Игнорируем свои сообщения
			if strings.HasPrefix(text, "[bot]") {
				return
			}

			// Команды
			switch strings.ToLower(text) {
			case "!online":
				onlineInfo := bm.IsOnline()
				rpc.BotSay(bm.FormatOnlineInfo(onlineInfo))

			case "!bt1":
				rpc.BotSay(fmt.Sprintf("Кнопка 1: %s", getButtonState(1)))

			case "!bt2":
				rpc.BotSay(fmt.Sprintf("Кнопка 2: %s", getButtonState(2)))
			}
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

	notifyScan := func(text string) {
		_ = rpc.SendTeamMessage(text, nil)
		log.Println(text)
	}

	bm.StartScan(1*time.Minute, notifyScan)

	defer bm.Stop()

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

func getButtonState(btnID int) string {
	// TODO: тут твоя логика запроса состояния кнопки
	return "не инициализирована"
}
