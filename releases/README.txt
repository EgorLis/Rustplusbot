RustPlusBot 1.1 - Windows x64

1. Extract the archive into a new folder.
2. Edit conf/rpconfig.json using the UUID, playerId and playerToken
   from your Rust+ server pairing notification.
3. Set auth_token_file to your saved rustplus.config.json.
   A relative path is resolved inside conf/.
4. Set the BattleMetrics server ID and API token in conf/bmconfig.json.
5. Add your own sound files to sounds/ if needed.
6. Run main.exe --check-connection to verify the Rust+ configuration.
7. Launch Start-RustBot.cmd or main.exe, then use !help in team chat.

The bot config starts empty. Bind switches and alarms through chat commands.
Logs are saved in logs/bot.log. Press Ctrl+C to stop.
Startup errors wait for Enter when a console is attached.

Read CONNECTION.md and RELEASE-NOTES.md before migrating from version 1.0.0.
Go and Node.js are not required to run the bot itself.

Build from source:
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-release.ps1 -Version 1.1

Use Go 1.27.4 or newer to build from source.
