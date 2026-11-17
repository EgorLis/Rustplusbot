# Local web panel

The experimental panel lives on branch `feature/local-web-ui`.

Place `scripts/Start-WebUI.cmd` and `scripts/Stop-WebUI.cmd` beside the GUI-enabled `main.exe`. The release builder includes both files automatically.

- Double-click `Start-WebUI.cmd` to run the panel, then open http://127.0.0.1:8787.
- The panel's **Stop** button stops only the bot; the panel stays available.
- Double-click `Stop-WebUI.cmd` to stop both the bot and the panel gracefully, including a panel running in the background.
- Closing the browser tab does not stop the panel.

The stop file identifies the RustPlusBot service on the selected local port. It does not kill processes by name and does not rely on an old PID. Running it again after shutdown is harmless.

For a custom port, use these commands from the executable's directory:

```powershell
.\Start-WebUI.cmd --ui-port 8790
.\Stop-WebUI.cmd --ui-port 8790
```

Equivalent direct commands:

```powershell
.\main.exe --ui
.\main.exe --stop-ui
```

Forms support connection settings, server players and tracking, switch bindings and on/off actions, alarms, death-watch and pairing-notification import. Bot settings apply without reconnecting; connection settings restart the bot's connection. Secrets remain in local config files, and blank password fields preserve saved values.

## Pairing notifications

Open **Rust+ notifications** and click **Listen**. Pair a server or device in the game: a new card appears automatically. The listener runs independently of the bot; once enabled, it resumes on the next panel launch. Stop a separate `npx ... fcm-listen` session before using it, because both use the same registered FCM device.

Server cards search BattleMetrics with the saved API token. Only exact normalized name matches are offered; if there are multiple matches, select the correct address yourself. You can also enter a BattleMetrics ID manually. **Apply server** saves pairing credentials, replaces the old BattleMetrics ID and reconnects a running bot. It does not silently switch servers on arrival.

Device cards let you explicitly assign a switch to BT1/BT2 or add an alarm. A device from another server cannot overwrite the current server's bindings. Rust+ has no request that lists every paired device: the inbox collects notifications received by this registration, rather than enumerating everything already paired in a phone app.

The listener uses Node.js and the existing `@liamcottle/push-receiver` dependency directly; running `npx fcm-listen` is no longer necessary. It automatically finds the package in the existing NPX cache. If that cache is absent, install the dependency beside the GUI executable:

```powershell
npm install --prefix . @liamcottle/push-receiver@0.0.3
```

Initial Steam/FCM registration is still a separate one-time step using the Rust+ registration tools. The panel reads `fcm_credentials` from the configured auth file. Renew that registration when necessary; the UI does not automate Steam login.

The private inbox is saved in `conf/webui-notifications.json`. It contains pairing tokens needed by the Apply button. Tokens are omitted from the inbox HTTP response and logs. Do not share this file or its backup.

## Map and death markers

Open **Map** after connecting the bot and load the server's map. The panel renders the Rust+ `GetMap` image with the protocol's pixel ocean margin. Wheel/buttons zoom, and dragging pans. Team positions refresh every five seconds.

The **Grid** toggle shows in-game cell references: columns A–Z, AA onward from west to east, and rows from 0 at the north edge. Cell count uses the [Rust client formula published by Facepunch](https://commits.facepunch.com/504506): `floor(worldSize * 7 / 1024)`. Cell width is `worldSize / cellCount`, with the grid anchored to the playable world, excluding image padding. Hover the map to see the current cell and world coordinates; player/death tooltips include their cell reference.

Select team members under **Death markers**. An enabled death-watch target is included automatically. BattleMetrics player IDs are different from SteamIDs; BattleMetrics supplies no in-game coordinates. Death markers are available only for team members exposed through Rust+.

The first team snapshot establishes a baseline. Later increases of `death_time`, or confirmed alive-to-dead transitions, create markers. If a player respawned between polls, their last known alive position is used and marked approximate. Missing coordinates produce a history entry without a map position. Deaths that happened while disconnected cannot always be reconstructed.

History is persisted in `conf/webui-map.json` and filtered by Rust+ server UUID and wipe timestamp. It is bounded to 500 entries. The map image is cached in memory and fetched on request.

Death markers display the player name and `DD.MM HH:mm` beside the cross and in the history, using the browser's local time zone. Hovering the marker still shows the full date, player name, grid cell and position accuracy.

Vending machine tracking is unavailable through the current standard Rust+ API. Facepunch [stopped sending vending machine and event map markers on 6 August 2026](https://rust.facepunch.com/news/power-trip). The schema still contains `GetMapMarkers` and `VendingMachine`, but that does not mean servers publish these markers. Tracking briefly broadcast shops would require a server that explicitly supplies this data, for example through an administrator-installed plugin.

References consulted: [Rust+ Desktop](https://github.com/Pronwan/rustplus-desktop) for API behaviour, and the [pinned RustPlusApi schema](internal/proto/README.md). The SVG renderer and tracker here are implemented in this project; no desktop source or image assets are bundled.

Frontend regression tests (Node.js built-ins, no additional dependencies):

```powershell
node --test internal/webui/test/*.test.cjs
```
