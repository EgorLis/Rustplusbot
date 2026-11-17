package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static/*
var assets embed.FS

func Run(ctx context.Context, conf string, port int, factory Factory) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	if port < 0 || port > 65535 {
		return errors.New("UI port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("open local UI: %w", err)
	}
	defer listener.Close()

	controller := New(ctx, conf, factory)
	previous := log.Writer()
	log.SetOutput(io.MultiWriter(previous, &controller.logs))
	defer log.SetOutput(previous)
	defer controller.Stop()
	defer controller.notifications.stop(false)
	go controller.mapObserver(ctx)
	if controller.notifications.disk.Enabled {
		if err := controller.notifications.start(); err != nil {
			log.Println("Rust+ notification listener:", err)
		}
	}

	server := &http.Server{Handler: handler(controller, listener.Addr().String(), stop), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
		case <-done:
		}
	}()

	log.Printf("Local web UI: http://%s", listener.Addr())
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func Handler(c *Controller, host string) http.Handler {
	return handler(c, host, nil)
}

func handler(c *Controller, host string, shutdown context.CancelFunc) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/map/load", func(w http.ResponseWriter, r *http.Request) {
		c.opMu.Lock()
		defer c.opMu.Unlock()
		respond(w, nil, c.LoadMap())
	})
	mux.HandleFunc("GET /api/map/image", func(w http.ResponseWriter, r *http.Request) {
		c.mapData.mu.Lock()
		defer c.mapData.mu.Unlock()
		if len(c.mapData.image) == 0 || r.URL.Query().Get("key") != c.mapData.key {
			http.Error(w, "map not loaded", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", c.mapData.contentType)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(c.mapData.image)
	})
	mux.HandleFunc("POST /api/listener/start", func(w http.ResponseWriter, r *http.Request) {
		c.opMu.Lock()
		defer c.opMu.Unlock()
		respond(w, nil, c.notifications.start())
	})
	mux.HandleFunc("POST /api/listener/stop", func(w http.ResponseWriter, r *http.Request) {
		c.opMu.Lock()
		defer c.opMu.Unlock()
		respond(w, nil, c.notifications.stop(true))
	})
	mux.HandleFunc("POST /api/notifications/apply", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID     string `json:"id"`
			Target string `json:"target"`
			BMID   string `json:"bm_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		respond(w, nil, c.ApplyNotification(input.ID, input.Target, input.BMID))
	})
	mux.HandleFunc("POST /api/notifications/search", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &input) {
			return
		}
		event, err := c.notifications.get(input.ID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if event.Kind != "server" {
			respond(w, nil, errors.New("это уведомление устройства"))
			return
		}
		c.notifications.search(input.ID)
		respond(w, nil, nil)
	})
	mux.HandleFunc("GET /api/identity", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]string{"application": "RustPlusBot Web UI"}, nil)
	})

	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if shutdown == nil {
			respond(w, nil, errors.New("panel shutdown is unavailable"))
			return
		}

		if err := c.Stop(); err != nil {
			respond(w, nil, err)
			return
		}

		respond(w, map[string]bool{"stopped": true}, nil)
		go shutdown()
	})

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		value, err := c.State()
		respond(w, value, err)
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var input Settings
		if !decode(w, r, &input) {
			return
		}
		respond(w, nil, c.SaveSettings(input))
	})
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) { respond(w, nil, c.Start()) })
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) { respond(w, nil, c.Stop()) })
	mux.HandleFunc("POST /api/action", func(w http.ResponseWriter, r *http.Request) {
		var input Action
		if !decode(w, r, &input) {
			return
		}
		value, err := c.Action(input)
		respond(w, value, err)
	})
	mux.HandleFunc("POST /api/players", func(w http.ResponseWriter, r *http.Request) {
		value, err := c.Players()
		respond(w, value, err)
	})
	mux.HandleFunc("POST /api/pairing", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Payload string `json:"payload"`
		}
		if !decode(w, r, &input) {
			return
		}
		value, err := parsePairing(input.Payload)
		respond(w, value, err)
	})
	mux.HandleFunc("GET /api/sounds", func(w http.ResponseWriter, r *http.Request) {
		files, err := os.ReadDir("sounds")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			respond(w, nil, err)
			return
		}
		names := []string{}
		for _, file := range files {
			if !file.IsDir() && strings.EqualFold(filepath.Ext(file.Name()), ".mp3") {
				names = append(names, file.Name())
			}
		}
		respond(w, names, nil)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := "index.html"
		if r.URL.Path != "/" {
			name = strings.TrimPrefix(r.URL.Path, "/")
			if name != "app.js" && name != "map.js" && name != "style.css" {
				http.NotFound(w, r)
				return
			}
		}
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		contentType := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "map.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"}
		w.Header().Set("Content-Type", contentType[name])
		w.Write(data)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RustPlusBot-UI", "1")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")

		// Host validation also blocks DNS rebinding. Cross-origin pages cannot operate the bot.
		if r.Host != host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}

		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		respond(w, nil, errors.New("некорректный запрос формы"))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		respond(w, nil, errors.New("ожидался один JSON-объект"))
		return false
	}
	return true
}

func respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"data": value})
}
