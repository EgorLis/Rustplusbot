RustPlusBot - test Web UI (1.2.0-webui.2) - Windows x64

1. Extract the archive into a new folder.
2. Double-click Start-WebUI.cmd.
3. Open http://127.0.0.1:8787 in your browser.
4. Open Settings and enter the server pairing credentials, the path to your
   Rust+ registration file, and optional BattleMetrics settings. Save them.
5. Click Start in the panel to connect the bot.

The Stop button stops the bot but leaves the panel available.
Stop-WebUI.cmd stops both the bot and the panel.
Closing your browser does not stop the panel.

For an existing installation, back up conf/ and sounds/, then copy them into
this new folder. Bundled configs are examples and contain no personal tokens.

The bot and web panel do not need Go or Node.js to run.
The optional Rust+ notification listener needs Node.js and push-receiver:
  npm install --prefix . @liamcottle/push-receiver@0.0.3
It can also reuse an existing NPX cache. Initial Steam/FCM registration is
still a separate step. See WEBUI.md and CONNECTION.md.

Read WEBUI.md and RELEASE-NOTES.md for features and current limitations.
Logs are saved in logs/bot.log. This is an experimental prerelease.
