package bot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

func TestStartContextRetriesAndCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 2 {
			cancel()
		}
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	host, portString, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portString)
	b := New()
	b.rpc = rpclient.New(host, port, 1, 1, false)
	if err := b.StartContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation after retry, got %v", err)
	}
	if attempts.Load() < 2 {
		t.Fatalf("initial failure was not retried: %d attempts", attempts.Load())
	}
	if b.stopCh != nil {
		t.Fatal("failed startup left bot marked as running")
	}
	b.Stop()
}

func TestStartContextCancelsBlockedHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	host, portString, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	b := New()
	b.rpc = rpclient.New(host, port, 1, 1, false)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := b.StartContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("cancelled connection did not stop promptly")
	}
}

func TestInitialConnectionStopsAtBudget(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	host, portString, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	b := New()
	b.rpc = rpclient.New(host, port, 1, 1, false)
	started := time.Now()
	if err := b.connectInitial(context.Background(), 150*time.Millisecond); err == nil {
		t.Fatal("offline startup succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup exceeded budget: %v", elapsed)
	}
	count := attempts.Load()
	time.Sleep(200 * time.Millisecond)
	if count == 0 || attempts.Load() != count {
		t.Fatal("startup kept trying after budget expired")
	}
}

func TestMissingBotConfigIsCreatedWithoutDeadlock(t *testing.T) {
	store := newConfigStore(t.TempDir() + "/botconfig.json")
	done := make(chan error, 1)
	go func() { done <- store.Load() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("creating missing config deadlocked")
	}
}
