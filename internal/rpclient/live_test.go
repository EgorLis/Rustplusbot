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

// This diagnostic reads team membership and chat availability; it never sends a message.
func TestLiveTeamChatReadOnly(t *testing.T) {
	file := os.Getenv("RUSTPLUS_LIVE_CONFIG")
	if file == "" {
		t.Skip("set RUSTPLUS_LIVE_CONFIG for a read-only live diagnostic")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cfg RustPlusConfig
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid config JSON")
	}
	client, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer client.Disconnect()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	team, err := client.SendRequestAsync(&AppRequest{GetTeamInfo: &AppEmpty{}}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GetTeamInfo: leader=%d; members=%d", team.GetTeamInfo().GetLeaderSteamId(), len(team.GetTeamInfo().GetMembers()))
	chat, err := client.SendRequestAsync(&AppRequest{GetTeamChat: &AppEmpty{}}, 5*time.Second)
	if err != nil {
		t.Logf("GetTeamChat unavailable: %v", err)
		return
	}
	t.Logf("GetTeamChat: messages=%d", len(chat.GetTeamChat().GetMessages()))
	markers, markerError := client.SendRequestAsync(&AppRequest{GetMapMarkers: &AppEmpty{}}, 5*time.Second)
	if markerError != nil {
		t.Logf("GetMapMarkers unavailable: %v", markerError)
	} else {
		counts := map[AppMarkerType]int{}
		for _, marker := range markers.GetMapMarkers().GetMarkers() {
			counts[marker.GetType()]++
		}
		t.Logf("GetMapMarkers: total=%d; types=%v", len(markers.GetMapMarkers().GetMarkers()), counts)
	}
	for _, message := range chat.GetTeamChat().GetMessages() {
		if len(message.GetMessage()) > 0 && message.GetMessage()[0] == '!' {
			t.Logf("Command in chat history: %q", message.GetMessage())
		}
	}
}
