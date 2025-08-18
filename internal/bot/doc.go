// Package bot — “склейка” вокруг rpclient, bmapi и mediahook, реализующая
// прикладного бота для Rust+. Бот:
//   - слушает Broadcast-сообщения и чат команды;
//   - обрабатывает команды (!help, !bt*, !alarm*, !track*, !death* и др.);
//   - управляет smart-switch’ами, реагирует на smart-alarm’ы;
//   - запускает death-watch (онлайн/оффлайн смерть персонажа);
//   - интегрируется с BattleMetrics для уведомлений о входе/выходе
//     отслеживаемых игроков;
//   - поддерживает конфиг (UseConfig/Save) и воспроизведение звуков
//     при событиях (через внешнюю программу ОС).
//
// Жизненный цикл:
//   - Создать бота через New().
//   - Передать клиентов: SetRustPlusClient(...), SetBattleMetrics(...),
//     (опционально) SetMediaHook(), SetCheckPlayerDeath(...).
//   - (Опционально) UseConfig("conf/botconfig.json") — применит BT/alarms/players.
//   - Запустить Start() и остановить Stop().
//
// Пример:
//
//	b := bot.New()
//	b.SetRustPlusClient(rpcfg)
//	b.SetBattleMetrics(bmcfg)   // необязательно
//	b.SetMediaHook()            // необязательно
//	_ = b.UseConfig("conf/botconfig.json")
//
//	if err := b.Start(); err != nil { log.Fatal(err) }
//	defer b.Stop()
//	select{} // держим процесс
//
// Конфигурация:
//   - хранится в JSON (см. BotConfig), включает BT1/BT2, список alarms и список
//     отслеживаемых игроков для BM. Команды в чате изменяют рантайм-состояние и
//     сразу сохраняют конфиг (команда !save доступна явно).
package bot
