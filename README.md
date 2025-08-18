# Rust Plus Bot

**RustPlusBot** — это автономный бот для **Rust+**, который:
- подключается к серверу через Rust+ (WebSocket);
- управляет смарт-девайсами (Switch/Alarm);
- реагирует на Smart Alarm (проигрывает звук/пишет в чат);
- перехватывает Bluetooth-кнопку (Volume Up/Down) и назначает на неё действия (например, включение/выключение свитчей);
- следит за онлайном выбранных игроков через BattleMetrics и присылает уведомления в чат;
- отслеживает смерти выбранного игрока (онлайн/оффлайн) через периодический опрос;
- настраивается прямо из внутриигрового чата командами `!…` и сохраняет состояние в конфиг.
Сообщения, отправленные самим ботом, помечаются префиксом `[bot]` и бот их игнорирует (чтобы не зациклиться).

## Возможности

- **Smart Switch**: включить/выключить, привязать две «кнопки» (BT1/BT2) к конкретным entity ID.
- **Smart Alarm**: добавлять/удалять алармы, кастомные сообщения и звуки (файлы `sounds/*.mp3`).
- **BattleMetrics tracking**: автоматические уведомления о входе/выходе отслеживаемых игроков.
- **Death-watch**: отслеживание смерти конкретного SteamID (вашего или тиммейта), с опциональным звуком.
- **Bluetooth-кнопка**: перехват системных клавиш громкости на Windows и привязка к действиям бота.
- **Команды в чате**: весь менеджмент (bt/alarms/track/death) делается прямо из тим-чата.
- **Конфиги**: все изменения сохраняются и применяются при перезапуске.

## Быстрый старт

Скачайте готовый релиз **1.0.0** с [GitHub Releases](https://github.com/EgorLis/Rustplusbot/releases).
В релизе есть исполняемый файл и две папки:
```text
rustplusbot.exe
conf/
   rpconfig.json
   bmconfig.json
   botconfig.json
sounds/
   1.mp3
   2.mp3
   3.mp3
```
1. **Заполни конфиги** в `conf/` (см. раздел «Настройка» ниже).
2. Убедись, что в `sounds/` лежат нужные mp3 (если используешь звуки).
3. Запусти `rustplusbot.exe`.
4. В игре открой тим-чат и введи `!help` — бот ответит списком команд.

## Настройка

**1) `rpconfig.json` — подключение к Rust+**

Нужны:
- IP/порт **app.port** сервера Rust,
- ваш **SteamID** (uint64),
- **PlayerToken** (int32),
- опция `use_proxy` (true — через Facepunch-прокси, false — прямое подключение к `ws://ip:app.port`).

**Как получить Rust+ токены** (через `npx @liamcottle/rustplus.js fcm-listen`)
1. Установи Node.js.
2. Открой PowerShell
3. Запусти следующие скрипты:

**Авторизация Rust+**
```bash
npx.cmd @liamcottle/rustplus.js fcm-register 
```
**Прослушивание подключений к серверу**
```bash
npx.cmd @liamcottle/rustplus.js fcm-listen 
```
4. Подключи нужный сервер к Rust+ (через игру и подтвержение через мобильное приложение).

5. В консоли появится `JSON`
Нас интересует именно следущие поля:
- "ip":"1.2.3.4"
- "port":"28888"
- "playerId":"7656......."
- "playerToken":"16......."

6. Заполни `conf/rpconfig.json`:
```
{
  "server": "1.2.3.4",         // IP сервера
  "port": 28888,               // port
  "player_id": 7656.......,    // playerId (uint64)
  "player_token": 16.......,   // playerToken (int32)
  "use_proxy": false           // true - через Facepunch proxy, false - прямое ws (я использовал прямое)
}
```
**2) `bmconfig.json` — BattleMetrics**

Чтобы бот умел сообщать о входе/выходе игроков и показывать `!track info`, нужен **персональный API-токен BM**:

Получи токен: `https://www.battlemetrics.com/developers/token`

Найди **ID сервера**: открой страницу сервера, URL вида

`https://www.battlemetrics.com/servers/rust/6803740` → бери цифры в конце (здесь `6803740`).
`conf/bmconfig.json:`
```json
{
  "server": "6803740",
  "token": "bm_xxx_your_token_here"
}
```
Игроков для отслеживания можно добавлять командами `!track add ...`(ниже).

**3) botconfig.json — состояние бота (опционально)**

Бот сам поддерживает этот конфиг: когда ты добавляешь/удаляешь свитчи/алармы/игроков командами, он всё сохраняет сюда. Пример:

```json
{
  "bt1": { "id": 559662, "name": "PVO" },
  "bt2": { "id": 559665, "name": "TURRETS" },
  "alarms": {
    "559684": { "id": 559684, "name": "boomAlarm", "msg": "дом_рейдят", "sound": "3.mp3" }
  },
  "players": [
    { "id": "1158097317", "name": "ToxicDude" }
  ]
}
```
При старте бот применяет содержимое `botconfig.json` (если включён).

**Как узнать ID девайсов (Switch/Alarm) в игре**

Самый простой способ — **нанести повреждение** (камнем) по устройству и посмотреть `combatlog` в консоли игры. В логе будет строка с **entityId** — это число нужно боту.

**Как узнать BattleMetricsID игрока**

Через BattleMetrics:

- открой страницу игрока вида `https://www.battlemetrics.com/players/1158097317` → числа в конце и есть его ID (`1158097317`).

(Для death-watch нужен именно SteamID64. Если у тебя только BattleMetricsID — открой профиль игрока в стиме, там обычно отображается 64-битный SteamID.)

## Взаимодействие в игре (команды чата)

Открой тим-чат и пиши команды (бот читает `Broadcast`).

Все свои сообщения бот шлёт с префиксом `[bot]` и игнорирует их.

### Справка
```bash
!help
```
### Кнопки/свитчи
```cpp
!bt status                      // показать состояние BT1/BT2
!bt1 set <id> <name>            // привязать BT1 к entity (switch)
!bt2 set <id> <name>            // привязать BT2 к entity (switch)
```
BT1/BT2 также можно дергать физическими кнопками (Bluetooth Volume Up/Down), если включён перехват в Windows.

### Алармы
```perl
!alarm list                     // список зарегистрированных alarm (id, msg, sound)
!alarm add <id> <name> [msg="..."] [sound=1.mp3|none]
!alarm del <id>                 // удалить alarm
!alarm mute                     // временно отключить реакции на все alarm
!alarm unmute                   // включить обратно
```
Примеры:
```sql
!alarm add 559684 boomAlarm msg="дом_рейдят" sound="3.mp3"
!alarm add 777001 doorAlarm msg="дом_рейдят"
!alarm add 777001 doorAlarm
```

### Отслеживание игроков (BattleMetrics)
```perl
!track list                               // показать текущий список отслеживаемых
!track info                               // кто из них онлайн/оффлайн прямо сейчас
!track add <battle_metrics_id> [name]     // добавить в список
!track del <battle_metrics_id>            // удалить из списка
```
Бот сам пишет в чат при входе/выходе отслеживаемых игроков (“➡/⬅ …”).

### Отслеживание смерти (death-watch)
```perl
!death set <steamid> [sound=1.mp3|none]  // выбрать цель и опциональный звук
!death start [interval_sec]              // начать опрос (по умолчанию 15 сек)
!death stop                              // остановить опрос
!death status                            // статус death-watch
```
Бот периодически запрашивает `TeamInfo` и сообщает: “**Я умер 💀**” (онлайн и оффлайн смерть), при наличии звука — открывает файл из `sounds/`.

### Сохранение конфига
```bash
!save
```

## Запуск из исходников
Требуется Go 1.21+ (желательно) и Windows (для модуля перехвата клавиш):
```bash
go build -o rustplusbot.exe
./rustplusbot.exe
```

Структура папок:
```text
conf/
  rpconfig.json
  bmconfig.json
  botconfig.json
sounds/
  1.mp3 2.mp3 3.mp3 ...
rustplusbot.exe
```

## Примечания и советы
- **Proxy vs Direct**: `use_proxy: true` — подключение через Facepunch proxy (TLS, ping/pong). `false` — напрямую к `ws://ip:app.port`; в этом режиме бот сам поддерживает “heartbeat”.
- **Звуки**: бот просто «открывает» файл через ОС (`xdg-open/open/start`), поэтому звук проигрывает ваша системная программа по умолчанию.
- **Bluetooth-кнопка (Windows)**: модуль `mediahook` перехватывает `VK_VOLUME_UP/DOWN`. Во время работы перехвата системная громкость не меняется.
- **[bot] префикс**: бот добавляет `[bot]` к своим сообщениям и не реагирует на такие сообщения.
- **ID девайсов**: удобно собрать через `combatlog` после удара по устройству.
- **BattleMetrics ETag**: клиент экономит запросы и корректно обрабатывает `304 Not Modified`.

## Благодарности
- Facepunch (Rust/Rust+)
- @liamcottle за отличный набор инструментов Rust+ (`@liamcottle/rustplus.js`)
- BattleMetrics за публичное API
