package webui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStopLocalStopsTheBotAndReleasesThePanelPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts, stops atomic.Int32
	done := make(chan error, 1)
	conf := t.TempDir()
	go func() {
		done <- Run(ctx, conf, port, func(dir string) (Runtime, error) {
			return &fakeRuntime{dir: dir, started: &starts, stopped: &stops}, nil
		})
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(base + "/api/identity")
		if err == nil {
			ready = response.StatusCode == http.StatusOK
			response.Body.Close()
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("panel did not start")
	}

	response, err := client.Post(base+"/api/start", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for deadline := time.Now().Add(time.Second); starts.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if starts.Load() != 1 {
		t.Fatal("bot did not start")
	}

	if err := StopLocal(context.Background(), port); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("panel stayed running after shutdown")
	}
	if stops.Load() != 1 {
		t.Fatal("panel shutdown did not clean up the bot exactly once")
	}

	if err := StopLocal(context.Background(), port); err != nil {
		t.Fatalf("repeated shutdown should be harmless: %v", err)
	}
}

func TestStopLocalDoesNotSendShutdownToAnotherApplication(t *testing.T) {
	var shutdownCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			shutdownCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, rawPort, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(rawPort)

	if err := StopLocal(context.Background(), port); err == nil {
		t.Fatal("unrelated server accepted as a RustPlusBot panel")
	}
	if shutdownCalls.Load() != 0 {
		t.Fatal("shutdown was sent to an unrelated server")
	}
}
