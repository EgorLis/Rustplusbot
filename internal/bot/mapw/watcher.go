package mapw

import (
	"context"
	"encoding/base64"
	"log"
	"math"
	"sync"

	"github.com/EgorLis/Rustplusbot/internal/rustplus"
)

type Watcher struct {
	rpc     *rustplus.Client
	mapData *MapData

	mu sync.RWMutex
}

type MapData struct {
	ImageBase64 string
	Monuments   []MonumentData
	Width       uint32
	Height      uint32
}

type MonumentData struct {
	Name string
	X    float64
	Y    float64
}

func NewMapWatcher(rpc *rustplus.Client) *Watcher {
	return &Watcher{
		rpc: rpc,
	}
}

func (w *Watcher) Init(ctx context.Context) {
	mapData, err := w.rpc.GetMapSync(ctx)

	for err != nil {
		log.Println("error getting map data: ", err)
		if ctx.Err() != nil {
			return
		}
		mapData, err = w.rpc.GetMapSync(ctx)
	}

	serverInfo, err := w.rpc.GetInfoSync(ctx)

	for err != nil {
		log.Println("error getting server info: ", err)
		if ctx.Err() != nil {
			return
		}
		serverInfo, err = w.rpc.GetInfoSync(ctx)
	}

	worldSize := 4000 // default world size
	if serverInfo != nil {
		worldSize = int(*serverInfo.MapSize)
	}

	// Water padding (обычно 2000 единиц воды вокруг карты)
	const padWorld = 2000
	totalWorld := worldSize + padWorld
	halfPad := float64(padWorld) * 0.5

	// Prepare data for template
	imageBase64 := base64.StdEncoding.EncodeToString(mapData.JpgImage)

	monuments := make([]MonumentData, 0, len(mapData.Monuments))
	for _, m := range mapData.Monuments {
		if *m.Name == "train_tunnel_display_name" {
			continue
		}

		// Convert world coordinates to percentage for web display
		x, y := worldToPercentage(float64(*m.X), float64(*m.Y), worldSize, halfPad, float64(totalWorld))

		monuments = append(monuments, MonumentData{
			Name: *m.Name,
			X:    x,
			Y:    y,
		})
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.mapData = &MapData{
		ImageBase64: imageBase64,
		Monuments:   monuments,
		Width:       *mapData.Width,
		Height:      *mapData.Height,
	}
}

func (w *Watcher) GetMapData() *MapData {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.mapData == nil {
		return nil
	}

	monuments := make([]MonumentData, len(w.mapData.Monuments))
	copy(monuments, w.mapData.Monuments)

	return &MapData{
		ImageBase64: w.mapData.ImageBase64,
		Monuments:   monuments,
		Width:       w.mapData.Width,
		Height:      w.mapData.Height,
	}
}

// worldToPercentage converts world coordinates (with water zone) to percentage for web display
// This mimics the logic from the C# code that handles the water padding around the map
func worldToPercentage(x, y float64, worldSize int, halfPad, totalWorld float64) (float64, float64) {
	// Clamp coordinates to the extended world bounds (including water)
	worldSizeFloat := float64(worldSize)
	minBound := -halfPad
	maxBound := worldSizeFloat + halfPad

	xx := math.Max(minBound, math.Min(maxBound, x))
	yy := math.Max(minBound, math.Min(maxBound, y))

	// Normalize to [0, 1] range considering the water padding
	// Shift by halfPad so that -halfPad becomes 0
	normalizedX := (xx + halfPad) / totalWorld
	normalizedY := (yy + halfPad) / totalWorld

	// Convert to percentage and invert Y for web display (Y grows downward in web)
	percentX := normalizedX * 100
	percentY := 100 - (normalizedY * 100)

	return percentX, percentY
}
