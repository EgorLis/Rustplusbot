package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

func TestWebEditsAndChatCommandsDoNotLoseEachOthersChanges(t *testing.T) {
	b := New()
	b.rpc = rpclient.New("localhost", 1, 1, 1, false)
	b.SetBattleMetrics(bmapi.BMConf{})
	path := filepath.Join(t.TempDir(), "botconfig.json")
	if err := b.UseConfig(path); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := b.UpdateConfig(func(cfg *BotConfig) error {
				cfg.Players = append(cfg.Players, PlayerConf{ID: fmt.Sprint(100 + i), Name: "web"})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := b.HandleCommand(fmt.Sprintf("!track add %d chat", 200+i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved BotConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Players) != 40 {
		t.Fatalf("concurrent edits lost players: %d", len(saved.Players))
	}
}

func TestDeathWatchConfigEditActuallyStartsAndStopsTheWatcher(t *testing.T) {
	b := New()
	b.rpc = rpclient.New("localhost", 1, 1, 1, false)
	if err := b.UseConfig(filepath.Join(t.TempDir(), "botconfig.json")); err != nil {
		t.Fatal(err)
	}
	defer b.StopDeathWatch()

	if err := b.UpdateConfig(func(cfg *BotConfig) error {
		cfg.Death = &DeathConfig{SteamID: "76561198101153603", Interval: 3600, Enabled: true, Sound: "none"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !b.RuntimeState().DeathRunning {
		t.Fatal("saved enabled flag did not start death-watch")
	}
	b.dwMu.Lock()
	selected := b.checkPlayerDeath.steamID
	b.dwMu.Unlock()
	if selected != 76561198101153603 {
		t.Fatal("watcher did not use the selected SteamID")
	}

	if err := b.UpdateConfig(func(cfg *BotConfig) error { cfg.Death.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if b.RuntimeState().DeathRunning {
		t.Fatal("saved disabled flag did not stop death-watch")
	}
}
