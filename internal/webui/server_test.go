package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type fakeRuntime struct {
	dir     string
	mu      sync.Mutex
	started *atomic.Int32
	stopped *atomic.Int32
	block   bool
}

func (f *fakeRuntime) StartContext(ctx context.Context) error {
	f.started.Add(1)
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (f *fakeRuntime) Stop() { f.stopped.Add(1) }
func (f *fakeRuntime) RuntimeState() bot.RuntimeState {
	return bot.RuntimeState{Connected: true, Server: "Test server"}
}
func (f *fakeRuntime) UpdateConfig(edit func(*bot.BotConfig) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg, err := loadConfig(f.dir)
	if err != nil {
		return err
	}
	if err := edit(&cfg.Bot); err != nil {
		return err
	}
	return writeJSON(f.dir, "botconfig.json", cfg.Bot)
}
func (f *fakeRuntime) SetSwitchValue(int, bool) error { return nil }
func (f *fakeRuntime) DeviceInfo(uint64) (*rpclient.AppEntityInfo, error) {
	return nil, fmt.Errorf("unknown device")
}
func (f *fakeRuntime) TeamInfo() (*rpclient.AppTeamInfo, error) { return &rpclient.AppTeamInfo{}, nil }
func (f *fakeRuntime) ServerPlayers() map[string]string {
	return map[string]string{"71": "Online player"}
}
func (f *fakeRuntime) RefreshServerPlayers() (map[string]string, error) {
	return f.ServerPlayers(), nil
}

func newTestController(t *testing.T, block bool) (*Controller, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var starts, stops atomic.Int32
	dir := t.TempDir()
	c := New(context.Background(), dir, func(dir string) (Runtime, error) {
		return &fakeRuntime{dir: dir, started: &starts, stopped: &stops, block: block}, nil
	})
	t.Cleanup(func() { c.Stop() })
	return c, &starts, &stops
}

func waitPhase(t *testing.T, c *Controller, phase string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		current := c.phase
		c.mu.Unlock()
		if current == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected phase %s", phase)
}

func testSettings() Settings {
	return Settings{ServerID: "c67b5845-738d-4edf-aac8-aa4f73284a93", PlayerID: "76561198101153601", PlayerToken: "-123456", BMServer: "31330805", BMToken: "private-bm-token"}
}

func TestHTTPRejectsForeignSitesAndDoesNotExposeSecrets(t *testing.T) {
	c, _, _ := newTestController(t, false)
	if err := c.SaveSettings(testSettings()); err != nil {
		t.Fatal(err)
	}
	handler := Handler(c, "127.0.0.1:8787")

	for _, item := range []struct {
		host, origin, content string
		want                  int
	}{
		{"attacker.test", "", "application/json", 403},
		{"127.0.0.1:8787", "https://attacker.test", "application/json", 403},
		{"127.0.0.1:8787", "", "application/x-www-form-urlencoded", 415},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+item.host+"/api/start", strings.NewReader("{}"))
		r.Header.Set("Origin", item.origin)
		r.Header.Set("Content-Type", item.content)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != item.want {
			t.Fatalf("cross-site request: %d", w.Code)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/state", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-bm-token") || strings.Contains(w.Body.String(), "-123456") {
		t.Fatal("credentials leaked in state")
	}
	if !strings.Contains(w.Body.String(), `"76561198101153601"`) {
		t.Fatal("SteamID was not encoded as an exact string")
	}
}

func TestHotEditsPersistWithoutRestartAndServerChangesRestart(t *testing.T) {
	c, starts, stops := newTestController(t, false)
	settings := testSettings()
	if err := c.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, c, "running")

	for _, input := range []Action{
		{Action: "track_add", ID: "71", Name: "Player with spaces"},
		{Action: "track_add", ID: "71", Name: "Updated name"},
		{Action: "switch_set", Slot: 1, ID: "1099511627899", Name: "Main base power"},
		{Action: "alarm_save", ID: "1099511627900", Name: "Raid alarm", Message: "Main base is being raided", Sound: "none"},
		{Action: "alarm_mute", Value: true},
		{Action: "death_save", ID: "76561198101153601", Interval: 9, Sound: "none", Enabled: true},
	} {
		if _, err := c.Action(input); err != nil {
			t.Fatalf("%s: %v", input.Action, err)
		}
	}

	cfg, err := loadConfig(c.conf)
	if err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || stops.Load() != 0 {
		t.Fatal("hot edit restarted the bot")
	}
	if len(cfg.Bot.Players) != 1 || cfg.Bot.Players[0].Name != "Updated name" {
		t.Fatal("tracking duplicated or was not persisted")
	}
	if cfg.Bot.BT1.ID != 1099511627899 || cfg.Bot.Alarms[1099511627900].Msg != "Main base is being raided" {
		t.Fatal("large ID or spaced message was lost")
	}
	if !cfg.Bot.AlarmsMuted || cfg.Bot.Death.Interval != 9 || !cfg.Bot.Death.Enabled {
		t.Fatal("runtime preferences were not saved")
	}
	players, err := c.Players()
	if err != nil || len(players) != 1 || players[0].ID != "71" {
		t.Fatal("server player listing failed")
	}

	settings.PlayerToken = ""
	settings.BMToken = ""
	if err := c.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 {
		t.Fatal("saving unchanged settings restarted the bot")
	}
	cfg, _ = loadConfig(c.conf)
	if cfg.Rust.PlayerToken != -123456 || cfg.BM.Token != "private-bm-token" {
		t.Fatal("blank password input cleared a secret")
	}

	settings.ServerID = "5cae741e-b47b-46d9-88f2-5cba9a60e1c7"
	if c.SaveSettings(settings) == nil {
		t.Fatal("server change accepted an old player token")
	}
	settings.PlayerToken = "321"
	if err := c.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, c, "running")
	if starts.Load() != 2 || stops.Load() != 1 {
		t.Fatal("server change did not stop and restart exactly once")
	}
}

func TestStopCancelsInitialConnectionAndPanelSurvives(t *testing.T) {
	c, starts, stops := newTestController(t, true)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for starts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := c.Start(); err == nil {
		t.Fatal("duplicate start accepted")
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, c, "stopped")
	if stops.Load() != 1 {
		t.Fatal("cancelled startup did not clean up")
	}
	if _, err := c.State(); err != nil {
		t.Fatal(err)
	}
}

func TestPairingImportPreservesNumericIDs(t *testing.T) {
	parsed, err := parsePairing(`{"type":"server","id":"c67b5845-738d-4edf-aac8-aa4f73284a93","playerId":76561198101153601,"playerToken":"123"}`)
	if err != nil || parsed["player_id"] != "76561198101153601" {
		t.Fatalf("server import: %v", err)
	}

	body := `{"type":"entity","entityId":18446744073709551614,"entityType":"1","entityName":"Main base"}`
	wrapped, _ := json.Marshal(map[string]any{"appData": []any{map[string]string{"key": "body", "value": body}}})
	parsed, err = parsePairing(string(wrapped))
	if err != nil || parsed["id"] != "18446744073709551614" || parsed["name"] != "Main base" {
		t.Fatalf("entity import: %v", err)
	}
}

func TestInvalidConfigActionDoesNotChangeDisk(t *testing.T) {
	c, _, _ := newTestController(t, false)
	if _, err := c.Action(Action{Action: "switch_set", Slot: 1, ID: "99", Name: "Valid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Action(Action{Action: "alarm_save", ID: "77", Name: "Bad sound", Sound: "../../file.mp3"}); err == nil {
		t.Fatal("sound path traversal accepted")
	}
	cfg, _ := loadConfig(c.conf)
	if len(cfg.Bot.Alarms) != 0 || cfg.Bot.BT1.ID != 99 {
		t.Fatal("invalid action changed saved settings")
	}
}
