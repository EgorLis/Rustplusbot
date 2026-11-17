package webui

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

//go:embed push-bridge.cjs
var pushBridge string

type PairingEvent struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	ServerID    string    `json:"server_id"`
	PlayerID    string    `json:"player_id,omitempty"`
	PlayerToken string    `json:"player_token,omitempty"` // Private on disk; omitted from API responses.
	EntityID    string    `json:"entity_id,omitempty"`
	EntityType  string    `json:"entity_type,omitempty"`
	Received    time.Time `json:"received"`
	Applied     bool      `json:"applied"`
	Matches     []BMMatch `json:"matches"`
	SearchError string    `json:"search_error,omitempty"`
	Searching   bool      `json:"searching"`
}

type notificationDisk struct {
	Enabled bool           `json:"enabled"`
	Events  []PairingEvent `json:"events"`
}

type notificationHub struct {
	mu        sync.Mutex
	ctx       context.Context
	conf      string
	disk      notificationDisk
	running   bool
	connected bool
	lastError string
	cancel    context.CancelFunc
	done      chan struct{}
}

func newNotificationHub(ctx context.Context, conf string) *notificationHub {
	h := &notificationHub{ctx: ctx, conf: conf}
	if err := readJSON(filepath.Join(conf, "webui-notifications.json"), &h.disk); err != nil {
		h.lastError = err.Error()
	}

	return h
}

func (h *notificationHub) saveLocked() error {
	return writeJSON(h.conf, "webui-notifications.json", h.disk)
}

func (h *notificationHub) snapshot() any {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := make([]PairingEvent, 0, len(h.disk.Events))
	for i := len(h.disk.Events) - 1; i >= 0; i-- {
		event := h.disk.Events[i]
		event.PlayerToken = ""
		events = append(events, event)
	}

	return map[string]any{"enabled": h.disk.Enabled, "running": h.running, "connected": h.connected, "error": h.lastError, "events": events}
}

func pushTools() (string, string, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		node = filepath.Join(os.Getenv("ProgramFiles"), "nodejs", "node.exe")
		if _, err := os.Stat(node); err != nil {
			return "", "", errors.New("для слушателя нужен установленный Node.js")
		}
	}

	exe, _ := os.Executable()
	candidates := []string{filepath.Join(filepath.Dir(exe), "node_modules", "@liamcottle", "push-receiver", "src", "client.js")}
	for _, root := range []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "npm-cache", "_npx"), filepath.Join(os.Getenv("APPDATA"), "npm-cache", "_npx")} {
		paths, _ := filepath.Glob(filepath.Join(root, "*", "node_modules", "@liamcottle", "push-receiver", "src", "client.js"))
		candidates = append(candidates, paths...)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return node, candidate, nil
		}
	}

	return "", "", errors.New("не найдена библиотека push-receiver; сначала установите инструменты Rust+ (см. WEBUI.md)")
}

func (h *notificationHub) start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return nil
	}

	if h.ctx.Err() != nil {
		return h.ctx.Err()
	}

	node, module, err := pushTools()
	if err != nil {
		h.lastError = err.Error()
		return err
	}

	cfg, err := loadConfig(h.conf)
	if err != nil {
		return err
	}

	file := cfg.Rust.AuthTokenFile
	if file == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		file = filepath.Join(home, "rustplus.config.json")
	} else if !filepath.IsAbs(file) {
		file = filepath.Join(h.conf, file)
	}

	var saved struct {
		Credentials json.RawMessage `json:"fcm_credentials"`
	}

	if err := readJSON(file, &saved); err != nil {
		return err
	}

	if len(saved.Credentials) == 0 {
		return errors.New("в файле регистрации нет fcm_credentials; сначала выполните вход в Rust+")
	}

	ctx, cancel := context.WithCancel(h.ctx)
	command := exec.CommandContext(ctx, node, "-e", pushBridge, module, file)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stderr = io.Discard // Do not forward third-party debug logs containing credentials.
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}

	if err := command.Start(); err != nil {
		cancel()
		return errors.New("не удалось запустить слушатель Node.js")
	}

	h.running, h.connected, h.lastError = true, false, ""
	h.cancel, h.done = cancel, make(chan struct{})
	h.disk.Enabled = true
	if err := h.saveLocked(); err != nil {
		cancel()
		command.Wait()
		h.running = false
		h.cancel = nil
		close(h.done)
		return err
	}

	done := h.done

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var message struct {
				Event   string `json:"event"`
				Key     string `json:"key"`
				Payload string `json:"payload"`
				Message string `json:"message"`
			}

			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}

			switch message.Event {
			case "connected", "disconnected":
				h.mu.Lock()
				h.connected = message.Event == "connected"
				h.mu.Unlock()
			case "error":
				h.mu.Lock()
				h.lastError = message.Message
				h.mu.Unlock()
			case "notification":
				if id, kind, err := h.receive(message.Key, message.Payload); err == nil && kind == "server" {
					go h.search(id)
				}
			}
		}

		err := command.Wait()
		h.mu.Lock()
		h.running, h.connected = false, false
		if err != nil && ctx.Err() == nil && h.lastError == "" {
			h.lastError = "слушатель завершился; проверьте регистрацию и подключение к сети"
		}

		close(done)
		h.mu.Unlock()
	}()
	return nil
}

func (h *notificationHub) stop(disable bool) error {
	h.mu.Lock()
	if disable {
		h.disk.Enabled = false
		if err := h.saveLocked(); err != nil {
			h.mu.Unlock()
			return err
		}
	}

	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return errors.New("слушатель ещё останавливается")
		}
	}

	return nil
}

func (h *notificationHub) receive(key, payload string) (string, string, error) {
	parsed, err := parsePairing(payload)
	if err != nil {
		return "", "", err
	} // Alarm-trigger pushes are not pairing events.
	if key == "" {
		key = payload
	}

	hash := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(hash[:12])
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, event := range h.disk.Events {
		if event.ID == id {
			return id, "", nil
		}
	}

	event := PairingEvent{ID: id, Kind: parsed["kind"], Name: parsed["name"], ServerID: parsed["server_id"], PlayerID: parsed["player_id"], PlayerToken: parsed["player_token"], EntityID: parsed["id"], EntityType: parsed["type"], Received: time.Now(), Matches: []BMMatch{}}
	if event.Kind == "server" {
		event.EntityID = ""
		event.Searching = true
	}

	h.disk.Events = append(h.disk.Events, event)
	if len(h.disk.Events) > 100 {
		h.disk.Events = h.disk.Events[len(h.disk.Events)-100:]
	}

	if err := h.saveLocked(); err != nil {
		return "", "", err
	}

	return id, event.Kind, nil
}

func (h *notificationHub) search(id string) {
	h.mu.Lock()
	name := ""
	for i := range h.disk.Events {
		if h.disk.Events[i].ID == id {
			name = h.disk.Events[i].Name
			h.disk.Events[i].Searching = true
		}
	}

	h.mu.Unlock()
	cfg, err := loadConfig(h.conf)
	var matches []BMMatch
	if err == nil {
		matches, err = searchBattleMetrics(h.ctx, name, cfg.BM.Token)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.disk.Events {
		if h.disk.Events[i].ID == id {
			h.disk.Events[i].Searching = false
			h.disk.Events[i].Matches = matches
			h.disk.Events[i].SearchError = ""
			if err != nil {
				h.disk.Events[i].SearchError = err.Error()
			}
		}
	}

	if err := h.saveLocked(); err != nil {
		h.lastError = err.Error()
	}
}

func (h *notificationHub) get(id string) (PairingEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, event := range h.disk.Events {
		if event.ID == id {
			return event, nil
		}
	}

	return PairingEvent{}, errors.New("уведомление не найдено")
}

func (h *notificationHub) applied(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.disk.Events {
		if h.disk.Events[i].ID == id {
			h.disk.Events[i].Applied = true
		}
	}

	return h.saveLocked()
}

func (c *Controller) ApplyNotification(id, target, bmID string) error {
	event, err := c.notifications.get(id)
	if err != nil {
		return err
	}

	if event.Kind == "server" {
		cfg, err := loadConfig(c.conf)
		if err != nil {
			return err
		}

		input := cfg.settings()
		input.ServerID, input.PlayerID, input.PlayerToken = event.ServerID, event.PlayerID, event.PlayerToken
		// Never carry the old server's BattleMetrics ID onto a newly paired server.
		input.BMServer = bmID
		if err := c.SaveSettings(input); err != nil {
			return err
		}
	} else {
		cfg, err := loadConfig(c.conf)
		if err != nil {
			return err
		}

		if event.ServerID == "" || event.ServerID != cfg.Rust.ServerID {
			return errors.New("сначала примените сервер, к которому относится это устройство")
		}

		name := event.Name
		if name == "" {
			name = "Устройство " + event.EntityID
		}

		input := Action{ID: event.EntityID, Name: name, Sound: "none"}
		switch target {
		case "bt1", "bt2":
			if event.EntityType != "1" && event.EntityType != "Switch" {
				return errors.New("уведомление не относится к Smart Switch")
			}

			input.Action = "switch_set"
			input.Slot = 1
			if target == "bt2" {
				input.Slot = 2
			}
		case "alarm":
			if event.EntityType != "2" && event.EntityType != "Alarm" {
				return errors.New("уведомление не относится к Smart Alarm")
			}

			input.Action = "alarm_save"
		default:
			return errors.New("выберите BT1, BT2 или аларм")
		}

		if _, err := c.Action(input); err != nil {
			return err
		}
	}

	return c.notifications.applied(id)
}
