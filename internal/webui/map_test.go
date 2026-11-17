package webui

import (
	"testing"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
	"google.golang.org/protobuf/proto"
)

func mapTeam(alive bool, death uint32, x, y float32) *rpclient.AppTeamInfo {
	return &rpclient.AppTeamInfo{Members: []*rpclient.AppTeamInfo_Member{{SteamId: proto.Uint64(76561198101153603), Name: proto.String("Teammate"), IsAlive: proto.Bool(alive), IsOnline: proto.Bool(true), DeathTime: proto.Uint32(death), X: proto.Float32(x), Y: proto.Float32(y)}}}
}

func TestDeathMarkersEstablishBaselineAndDeduplicate(t *testing.T) {
	var store mapDataStore
	watch := map[string]bool{"76561198101153603": true}
	if store.consume("server", 10, mapTeam(false, 100, 100, 200), watch) {
		t.Fatal("a death from before connecting was emitted")
	}
	store.consume("server", 10, mapTeam(true, 100, 300, 400), watch)
	if !store.consume("server", 10, mapTeam(false, 200, 320, 410), watch) {
		t.Fatal("new death was not recorded")
	}
	if store.consume("server", 10, mapTeam(false, 200, 320, 410), watch) {
		t.Fatal("same death was emitted again")
	}
	if len(store.history) != 1 || *store.history[0].X != 320 || store.history[0].Estimated {
		t.Fatal("wrong death coordinates")
	}
}

func TestDeathMarkerUsesLastAlivePositionAfterMissedRespawn(t *testing.T) {
	var store mapDataStore
	watch := map[string]bool{"76561198101153603": true}
	store.consume("server", 10, mapTeam(true, 100, 300, 400), watch)
	if !store.consume("server", 10, mapTeam(true, 200, 1000, 2000), watch) {
		t.Fatal("death between polls was missed")
	}
	entry := store.history[0]
	if !entry.Estimated || *entry.X != 300 || *entry.Y != 400 {
		t.Fatal("respawn location was incorrectly used as the death location")
	}
}

func TestUntrackedDeathsAndMissingOfflineAliveFieldDoNotCreateMarkers(t *testing.T) {
	var store mapDataStore
	store.consume("server", 10, mapTeam(true, 0, 300, 400), nil)
	store.consume("server", 10, mapTeam(false, 100, 300, 400), nil)
	if len(store.history) != 0 {
		t.Fatal("untracked player created a marker")
	}
	var next mapDataStore
	watch := map[string]bool{"76561198101153603": true}
	next.consume("server", 10, mapTeam(true, 0, 300, 400), watch)
	team := mapTeam(false, 0, 0, 0)
	team.Members[0].IsAlive = nil
	team.Members[0].IsOnline = proto.Bool(false)
	if next.consume("server", 10, team, watch) {
		t.Fatal("missing offline fields created a false death")
	}
}

func TestMapHistoryIsSeparatedByServerAndWipe(t *testing.T) {
	c, _, _ := newTestController(t, false)
	c.mapData.history = []DeathMarker{{ID: "one", ServerID: "a", Wipe: 1}, {ID: "two", ServerID: "a", Wipe: 2}, {ID: "three", ServerID: "b", Wipe: 1}}
	view := c.mapSnapshot("a", 1, 0).(map[string]any)
	entries := view["deaths"].([]DeathMarker)
	if len(entries) != 1 || entries[0].ID != "one" {
		t.Fatal("death history leaked across server or wipe")
	}
}
