# Current Rust+ connection

The bot uses `wss://companion-rust.facepunch.com/socket/{server_id}` with the `rustplus.auth.v1` subprotocol and the base64url-encoded companion token. Old `server`, `port` and `use_proxy` config fields no longer select the bot's connection route.

Example `conf/rpconfig.json`:

```json
{
  "server_id": "UUID from your server pairing notification",
  "player_id": 76561198000000000,
  "player_token": 123456,
  "auth_token_file": "C:/Users/your-user/rustplus.config.json"
}
```

The auth file must contain `rustplus_auth_token`, as saved by registration. Relative `auth_token_file` paths resolve against the config directory. If omitted, the default is `rustplus.config.json` in the user's home directory. The companion token is reloaded for every connection attempt, allowing a renewed saved login to take effect during reconnects. Credentials are not logged.

The first connection has a total budget of 60 seconds, including retries and protocol verification. Invalid configuration or rejected credentials stop startup immediately. After the first verified `GetInfo` reply, reconnects continue with delays of 1 to 30 seconds. A protocol heartbeat keeps idle sessions active.

Errors appear in the console and `logs/bot.log`. Startup errors wait for Enter when an interactive console is attached. Ctrl+C stops the bot normally. Redirected/noninteractive runs return an error exit code without waiting for input.

To check a configuration without hooks, chat messages or device actions:

```powershell
.\main.exe --check-connection --config-dir .\conf
```

The updated schema, its pinned upstream source and regeneration instructions are documented in [internal/proto/README.md](internal/proto/README.md).
