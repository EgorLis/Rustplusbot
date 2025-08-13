// Package rpbot — «мозг» бота: чат‑команды, смарт‑устройства, death‑watch,
// загрузка/сохранение конфигурации и реакция на события из Rust+.
//
// Высокоуровневый сценарий:
//
//	 b := bot.New()
//		b.SetRustPlusClient(rpcfg)
//		b.SetBattleMetrics(bmcfg)
//		b.SetMediaHook()
//		b.SetCheckPlayerDeath(rpcfg.PlayerID, &sound)
//
//	 подключим конфиг бота и применим его (alarms/switches/players)
//
//		bot.UseConfig("conf/botconfig.json")
//		bot.Start()
//	 defer bot.Stop()
package bot
