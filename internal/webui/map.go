package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type MapMember struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	Online bool     `json:"online"`
	Alive  bool     `json:"alive"`
}

type DeathMarker struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Wipe      uint32    `json:"wipe"`
	PlayerID  string    `json:"player_id"`
	Name      string    `json:"name"`
	At        time.Time `json:"at"`
	X         *float64  `json:"x"`
	Y         *float64  `json:"y"`
	Estimated bool      `json:"estimated"`
}

type teamSample struct {
	Alive bool
	Death uint32
	X, Y  *float64
}

type mapDataStore struct {
	mu            sync.Mutex
	pollMu        sync.Mutex
	key           string
	width, height int
	margin        int32
	worldSize     uint32
	image         []byte
	contentType   string
	stream        string
	samples       map[string]teamSample
	team          []MapMember
	history       []DeathMarker
	historyLoaded bool
	lastError     string
}

type mapRuntime interface {
	MapData() (*rpclient.AppMap, uint32, error)
}

func mapKey(server string, wipe uint32) string { return fmt.Sprintf("%s/%d", server, wipe) }

func (c *Controller) LoadMap() error {
	c.mu.Lock()
	runtime, phase, generation := c.current, c.phase, c.generation
	c.mu.Unlock()
	if runtime == nil || phase != "running" {
		return errors.New("сначала подключите бота")
	}

	source, ok := runtime.(mapRuntime)
	if !ok {
		return errors.New("этот клиент не поддерживает карту")
	}

	cfg, err := loadConfig(c.conf)
	if err != nil {
		return err
	}

	wipe := runtime.RuntimeState().WipeTime
	mapData, size, err := source.MapData()
	if err != nil {
		return err
	}

	if size == 0 {
		return errors.New("сервер не сообщил размер игрового мира")
	}

	geometry, _, err := image.DecodeConfig(bytes.NewReader(mapData.GetJpgImage()))
	if err != nil {
		return errors.New("сервер вернул некорректное изображение карты")
	}

	margin := mapData.GetOceanMargin()
	if margin < 0 || geometry.Width <= 2*int(margin) || geometry.Height <= 2*int(margin) || geometry.Width > 16384 || geometry.Height > 16384 {
		return errors.New("некорректная геометрия карты Rust+")
	}

	if geometry.Width != int(mapData.GetWidth()) || geometry.Height != int(mapData.GetHeight()) {
		return errors.New("размер изображения не совпадает с данными карты Rust+")
	}

	c.mu.Lock()
	if c.generation != generation || c.phase != "running" {
		c.mu.Unlock()
		return errors.New("подключение изменилось во время загрузки карты")
	}

	c.mu.Unlock()
	c.mapData.mu.Lock()
	c.mapData.key = mapKey(cfg.Rust.ServerID, wipe)
	c.mapData.image = append([]byte{}, mapData.GetJpgImage()...)
	c.mapData.contentType = http.DetectContentType(mapData.GetJpgImage())
	c.mapData.width, c.mapData.height = geometry.Width, geometry.Height
	c.mapData.margin, c.mapData.worldSize = margin, size
	c.mapData.lastError = ""
	c.mapData.mu.Unlock()
	return nil
}

func (c *Controller) mapSnapshot(server string, wipe uint32, generation uint64) any {
	c.mapData.mu.Lock()
	defer c.mapData.mu.Unlock()
	key := mapKey(server, wipe)
	deaths := []DeathMarker{}
	for i := len(c.mapData.history) - 1; i >= 0; i-- {
		entry := c.mapData.history[i]
		if entry.ServerID == server && entry.Wipe == wipe {
			deaths = append(deaths, entry)
		}
	}

	team := []MapMember{}
	if c.mapData.stream != "" && c.mapData.stream == fmt.Sprintf("%s/%d", key, generation) {
		team = append(team, c.mapData.team...)
	}

	return map[string]any{"loaded": c.mapData.key == key && len(c.mapData.image) > 0, "key": key, "width": c.mapData.width, "height": c.mapData.height, "ocean_margin": c.mapData.margin, "world_size": c.mapData.worldSize, "team": team, "deaths": deaths, "error": c.mapData.lastError}
}

func (c *Controller) mapObserver(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.observeMap()
		}
	}
}

func (c *Controller) observeMap() {
	c.mapData.pollMu.Lock()
	defer c.mapData.pollMu.Unlock()
	c.mu.Lock()
	runtime, phase, generation := c.current, c.phase, c.generation
	c.mu.Unlock()
	if runtime == nil || phase != "running" || !runtime.RuntimeState().Connected {
		return
	}

	cfg, err := loadConfig(c.conf)
	if err != nil {
		return
	}

	team, err := runtime.TeamInfo()
	if err != nil {
		return
	}

	c.mu.Lock()
	current := c.generation == generation && c.phase == "running"
	c.mu.Unlock()
	if !current {
		return
	}

	wipe := runtime.RuntimeState().WipeTime
	watch := map[string]bool{}
	for _, id := range cfg.Bot.MapWatch {
		watch[id] = true
	}

	if cfg.Bot.Death != nil && cfg.Bot.Death.Enabled {
		watch[cfg.Bot.Death.SteamID] = true
	}

	c.mapData.mu.Lock()
	defer c.mapData.mu.Unlock()
	if !c.mapData.historyLoaded {
		var saved struct {
			Deaths []DeathMarker `json:"deaths"`
		}

		if err := readJSON(filepath.Join(c.conf, "webui-map.json"), &saved); err != nil {
			c.mapData.lastError = err.Error()
		}

		c.mapData.history = saved.Deaths
		c.mapData.historyLoaded = true
	}

	stream := fmt.Sprintf("%s/%d", mapKey(cfg.Rust.ServerID, wipe), generation)
	if c.mapData.stream != stream {
		c.mapData.stream = stream
		c.mapData.samples = map[string]teamSample{}
	}

	newDeaths := c.mapData.consume(cfg.Rust.ServerID, wipe, team, watch)
	if newDeaths {
		if len(c.mapData.history) > 500 {
			c.mapData.history = c.mapData.history[len(c.mapData.history)-500:]
		}

		if err := writeJSON(c.conf, "webui-map.json", map[string]any{"deaths": c.mapData.history}); err != nil {
			c.mapData.lastError = "не удалось сохранить метки смерти"
		}
	}
}

func coordinate(value *float32) *float64 {
	if value == nil {
		return nil
	}

	n := float64(*value)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}

	return &n
}

// Successive team snapshots establish a baseline before any death can be emitted.
func (m *mapDataStore) consume(server string, wipe uint32, team *rpclient.AppTeamInfo, watch map[string]bool) bool {
	if m.samples == nil {
		m.samples = map[string]teamSample{}
	}

	m.team = []MapMember{}
	changed := false
	for _, member := range team.GetMembers() {
		id := strconv.FormatUint(member.GetSteamId(), 10)
		x, y := coordinate(member.X), coordinate(member.Y)
		before, seen := m.samples[id]
		next := teamSample{Alive: member.GetIsAlive(), Death: member.GetDeathTime(), X: before.X, Y: before.Y}
		if member.GetIsAlive() && x != nil && y != nil {
			next.X, next.Y = x, y
		}

		m.team = append(m.team, MapMember{ID: id, Name: member.GetName(), X: x, Y: y, Online: member.GetIsOnline(), Alive: member.GetIsAlive()})
		newDeath := seen && watch[id] && ((next.Death > before.Death && next.Death > 0) || (before.Alive && !next.Alive && member.IsAlive != nil && member.GetIsOnline()))
		if newDeath {
			dx, dy := before.X, before.Y
			estimated := true
			if !next.Alive && x != nil && y != nil && (*x != 0 || *y != 0) {
				dx, dy = x, y
				estimated = false
			}

			at := time.Now()
			if next.Death > 0 {
				at = time.Unix(int64(next.Death), 0)
			}

			hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s/%d", server, wipe, id, at.UnixNano())))
			entry := DeathMarker{ID: hex.EncodeToString(hash[:12]), ServerID: server, Wipe: wipe, PlayerID: id, Name: member.GetName(), At: at, X: dx, Y: dy, Estimated: estimated}
			duplicate := false
			for _, old := range m.history {
				if old.ID == entry.ID {
					duplicate = true
					break
				}
			}

			if !duplicate {
				m.history = append(m.history, entry)
				changed = true
			}
		}

		m.samples[id] = next
	}

	return changed
}
