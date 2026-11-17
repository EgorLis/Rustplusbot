package rpclient

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Opt in explicitly: this sends only GetInfo, GetTeamInfo and GetTime requests.
func TestLiveCompanionConnection(t *testing.T) {
	configPath := os.Getenv("RUSTPLUS_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("set RUSTPLUS_LIVE_CONFIG to opt into a read-only live check")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg RustPlusConfig
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid live config JSON")
	}
	rp, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan struct{}, 4)
	rp.OnConnected = func() { connections <- struct{}{} }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	defer rp.Disconnect()
	if err := rp.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	await(t, connections, "live verified connection")
	t.Logf("Verified server: %s", rp.ServerInfo().GetName())
	response, err := rp.SendRequestAsync(&AppRequest{GetTeamInfo: &AppEmpty{}}, 5*time.Second)
	if err != nil {
		t.Fatalf("live GetTeamInfo: %v", err)
	}
	if response.TeamInfo == nil {
		t.Fatal("GetTeamInfo returned no team")
	}
	t.Logf("GetTeamInfo decoded: %d members", len(response.TeamInfo.Members))
	if _, err := rp.SendRequestAsync(&AppRequest{GetTime: &AppEmpty{}}, 5*time.Second); err != nil {
		t.Fatalf("live GetTime: %v", err)
	}
	// Wait past the heartbeat tick and the original connect deadline.
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(27 * time.Second):
	}
	if _, err := rp.SendRequestAsync(&AppRequest{GetInfo: &AppEmpty{}}, 5*time.Second); err != nil {
		t.Fatalf("session after heartbeat: %v", err)
	}
	t.Log("Session remained usable after heartbeat")
	rp.closeConn() // Simulate a lost transport without logging the user out.
	await(t, connections, "live reconnect")
	if _, err := rp.SendRequestAsync(&AppRequest{GetTeamInfo: &AppEmpty{}}, 5*time.Second); err != nil {
		t.Fatalf("team request after reconnect: %v", err)
	}
	t.Log("Reconnected and verified a fresh team response")
}
