package webui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNotificationsArePersistedDeduplicatedAndTokensRedacted(t *testing.T) {
	dir := t.TempDir()
	h := newNotificationHub(context.Background(), dir)
	payload := `{"type":"server","id":"c67b5845-738d-4edf-aac8-aa4f73284a93","name":"Test server","playerId":"76561198101153603","playerToken":"-987654321"}`
	id, kind, err := h.receive("push-1", payload)
	if err != nil || kind != "server" {
		t.Fatal(err)
	}
	if _, _, err := h.receive("push-1", payload); err != nil {
		t.Fatal(err)
	}
	if len(h.disk.Events) != 1 {
		t.Fatal("duplicate push created two entries")
	}
	view, _ := json.Marshal(h.snapshot())
	if strings.Contains(string(view), "-987654321") {
		t.Fatal("pairing token leaked in the inbox API")
	}
	reloaded := newNotificationHub(context.Background(), dir)
	event, err := reloaded.get(id)
	if err != nil || event.PlayerToken != "-987654321" {
		t.Fatal("private pairing credentials were not persisted")
	}
}

func TestApplyingNewServerDoesNotKeepOldBattleMetricsServer(t *testing.T) {
	c, _, _ := newTestController(t, false)
	if err := c.SaveSettings(testSettings()); err != nil {
		t.Fatal(err)
	}
	id, _, err := c.notifications.receive("server-2", `{"type":"server","id":"5cae741e-b47b-46d9-88f2-5cba9a60e1c7","name":"Another server","playerId":"76561198101153603","playerToken":"456"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyNotification(id, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig(c.conf)
	if cfg.Rust.ServerID != "5cae741e-b47b-46d9-88f2-5cba9a60e1c7" || cfg.Rust.PlayerToken != 456 || cfg.BM.Server != "" {
		t.Fatal("server application retained stale pairing or BattleMetrics ID")
	}
}

func TestDeviceNotificationRequiresItsOwnServer(t *testing.T) {
	c, _, _ := newTestController(t, false)
	if err := c.SaveSettings(testSettings()); err != nil {
		t.Fatal(err)
	}
	id, _, err := c.notifications.receive("entity-1", `{"type":"entity","id":"5cae741e-b47b-46d9-88f2-5cba9a60e1c7","entityId":"1099511627899","entityType":"1","entityName":"Base power"}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.ApplyNotification(id, "bt1", "") == nil {
		t.Fatal("device from another server was applied")
	}
	id, _, err = c.notifications.receive("entity-2", `{"type":"entity","id":"c67b5845-738d-4edf-aac8-aa4f73284a93","entityId":"1099511627899","entityType":"1","entityName":"Base power"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyNotification(id, "bt1", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig(c.conf)
	if cfg.Bot.BT1 == nil || cfg.Bot.BT1.ID != 1099511627899 {
		t.Fatal("large paired device ID was lost")
	}
}
