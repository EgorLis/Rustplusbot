package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/rustplus"
)

type MapData struct {
	ImageBase64 string
	Monuments   []MonumentData
	Width       uint32
	Height      uint32
	CellsCount  int
}

type MonumentData struct {
	Name string
	X    float64
	Y    float64
}

const mapTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0, user-scalable=no">
    <title>Rust+ Map Viewer</title>
    <style>
        body {
            margin: 0;
            padding: 20px;
            font-family: Arial, sans-serif;
            background: #f0f0f0;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c3e50;
            color: white;
            padding: 20px;
            text-align: center;
        }
        .map-wrapper {
            position: relative;
            width: 100%;
            overflow: hidden;
            background: #1a1a1a;
            cursor: grab;
        }
        .map-wrapper:active {
            cursor: grabbing;
        }
        .map-container {
            position: relative;
            width: 100%;
            transform-origin: 0 0;
            transition: transform 0.1s ease-out;
        }
        .map-image {
            width: 100%;
            height: auto;
            display: block;
            pointer-events: none;
        }
        .monuments-layer {
            position: absolute;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            pointer-events: none;
        }
        .monument {
            position: absolute;
            width: 12px;
            height: 12px;
            background: #e74c3c;
            border: 2px solid white;
            border-radius: 50%;
            cursor: pointer;
            box-shadow: 0 2px 4px rgba(0,0,0,0.3);
            transform: translate(-50%, -50%);
            pointer-events: auto;
            transition: transform 0.2s;
        }
        .monument:hover {
            background: #c0392b;
            transform: translate(-50%, -50%) scale(1.2);
        }
        .monument-label {
            position: absolute;
            background: rgba(0,0,0,0.8);
            color: white;
            padding: 2px 6px;
            border-radius: 3px;
            font-size: 11px;
            white-space: nowrap;
            transform: translate(15px, -50%);
            pointer-events: none;
        }
        .controls {
            position: absolute;
            bottom: 20px;
            right: 20px;
            z-index: 10;
            display: flex;
            gap: 10px;
        }
        .zoom-btn {
            background: rgba(0,0,0,0.7);
            color: white;
            border: none;
            width: 40px;
            height: 40px;
            border-radius: 50%;
            font-size: 20px;
            cursor: pointer;
            display: flex;
            align-items: center;
            justify-content: center;
            transition: background 0.2s;
        }
        .zoom-btn:hover {
            background: rgba(0,0,0,0.9);
        }
        .zoom-level {
            background: rgba(0,0,0,0.7);
            color: white;
            padding: 5px 10px;
            border-radius: 20px;
            font-size: 12px;
            display: flex;
            align-items: center;
        }
        .reset-btn {
            background: rgba(0,0,0,0.7);
            color: white;
            border: none;
            padding: 5px 15px;
            border-radius: 20px;
            cursor: pointer;
            font-size: 12px;
        }
        .reset-btn:hover {
            background: rgba(0,0,0,0.9);
        }
        .info {
            padding: 20px;
            background: #f8f9fa;
            border-top: 1px solid #dee2e6;
        }
        .refresh-btn {
            background: #3498db;
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
        }
        .refresh-btn:hover {
            background: #2980b9;
        }
        .coordinates {
            font-family: monospace;
            font-size: 12px;
            color: #666;
            margin-top: 10px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🗺️ Rust+ Map Viewer</h1>
            <p>Interactive map with monuments (Zoom & Pan)</p>
        </div>

        <div class="map-wrapper" id="mapWrapper">
            <div class="map-container" id="mapContainer">
                <img src="data:image/jpeg;base64,{{.ImageBase64}}" alt="Map" class="map-image" id="mapImage">
                <div class="monuments-layer" id="monumentsLayer">
                    {{range $index, $monument := .Monuments}}
                    <div class="monument" data-x="{{printf "%.2f" $monument.X}}" data-y="{{printf "%.2f" $monument.Y}}" style="left: {{printf "%.2f" $monument.X}}%; top: {{printf "%.2f" $monument.Y}}%;">
                        <div class="monument-label">{{$monument.Name}}</div>
                    </div>
                    {{end}}
                </div>
            </div>
            <div class="controls">
                <button class="zoom-btn" id="zoomInBtn">+</button>
                <div class="zoom-level" id="zoomLevel">100%</div>
                <button class="zoom-btn" id="zoomOutBtn">-</button>
                <button class="reset-btn" id="resetBtn">Reset</button>
            </div>
        </div>

        <div class="info">
            <p><strong>Map Size:</strong> {{.Width}} x {{.Height}} pixels</p>
            <p><strong>Monuments:</strong> {{len .Monuments}}</p>
            <div class="coordinates" id="coordinates">Position: x=0, y=0 | Zoom: 1.0x</div>
            <button class="refresh-btn" onclick="location.reload()">🔄 Refresh Map</button>
        </div>
    </div>

    <script>
        class MapZoom {
            constructor() {
                this.wrapper = document.getElementById('mapWrapper');
                this.container = document.getElementById('mapContainer');
                this.monumentsLayer = document.getElementById('monumentsLayer');
                this.zoomLevel = 1;
                this.minZoom = 0.5;
                this.maxZoom = 3;
                this.translateX = 0;
                this.translateY = 0;
                
                this.isDragging = false;
                this.startX = 0;
                this.startY = 0;
                this.lastX = 0;
                this.lastY = 0;
                
                this.init();
            }
            
            init() {
                this.updateTransform();
                this.setupEventListeners();
                this.setupWheelZoom();
            }
            
            setupEventListeners() {
                document.getElementById('zoomInBtn').addEventListener('click', () => this.zoomIn());
                document.getElementById('zoomOutBtn').addEventListener('click', () => this.zoomOut());
                document.getElementById('resetBtn').addEventListener('click', () => this.reset());
                
                this.wrapper.addEventListener('mousedown', (e) => this.startDrag(e));
                window.addEventListener('mousemove', (e) => this.drag(e));
                window.addEventListener('mouseup', () => this.stopDrag());
                
                this.wrapper.addEventListener('touchstart', (e) => this.startDrag(e));
                window.addEventListener('touchmove', (e) => this.drag(e));
                window.addEventListener('touchend', () => this.stopDrag());
            }
            
            setupWheelZoom() {
                this.wrapper.addEventListener('wheel', (e) => {
                    e.preventDefault();
                    const delta = e.deltaY > 0 ? -0.1 : 0.1;
                    const rect = this.wrapper.getBoundingClientRect();
                    const mouseX = (e.clientX - rect.left) / rect.width;
                    const mouseY = (e.clientY - rect.top) / rect.height;
                    this.zoom(delta, mouseX, mouseY);
                });
            }
            
            startDrag(e) {
                this.isDragging = true;
                const clientX = e.clientX || (e.touches && e.touches[0].clientX);
                const clientY = e.clientY || (e.touches && e.touches[0].clientY);
                this.startX = clientX - this.translateX;
                this.startY = clientY - this.translateY;
                this.wrapper.style.cursor = 'grabbing';
            }
            
            drag(e) {
                if (!this.isDragging) return;
                e.preventDefault();
                const clientX = e.clientX || (e.touches && e.touches[0].clientX);
                const clientY = e.clientY || (e.touches && e.touches[0].clientY);
                this.translateX = clientX - this.startX;
                this.translateY = clientY - this.startY;
                this.updateTransform();
            }
            
            stopDrag() {
                this.isDragging = false;
                this.wrapper.style.cursor = 'grab';
            }
            
            zoomIn() {
                this.zoom(0.2);
            }
            
            zoomOut() {
                this.zoom(-0.2);
            }
            
            zoom(delta, centerX = 0.5, centerY = 0.5) {
                const oldZoom = this.zoomLevel;
                let newZoom = oldZoom + delta;
                newZoom = Math.max(this.minZoom, Math.min(this.maxZoom, newZoom));
                
                if (newZoom === oldZoom) return;
                
                const wrapperRect = this.wrapper.getBoundingClientRect();
                const containerRect = this.container.getBoundingClientRect();
                
                const xPercent = centerX;
                const yPercent = centerY;
                
                const pointX = xPercent * containerRect.width;
                const pointY = yPercent * containerRect.height;
                
                this.zoomLevel = newZoom;
                
                const newContainerWidth = containerRect.width * (this.zoomLevel / oldZoom);
                const newContainerHeight = containerRect.height * (this.zoomLevel / oldZoom);
                
                const newPointX = xPercent * newContainerWidth;
                const newPointY = yPercent * newContainerHeight;
                
                this.translateX += pointX - newPointX;
                this.translateY += pointY - newPointY;
                
                this.updateTransform();
            }
            
            updateTransform() {
                var transform = "translate(" + this.translateX + "px, " + this.translateY + "px) scale(" + this.zoomLevel + ")";
                this.container.style.transform = transform;
                
                document.getElementById('zoomLevel').innerText = Math.round(this.zoomLevel * 100) + '%';
                document.getElementById('coordinates').innerHTML = "Position: x=" + Math.round(this.translateX) + ", y=" + Math.round(this.translateY) + " | Zoom: " + this.zoomLevel.toFixed(2) + "x";
            }
            
            reset() {
                this.zoomLevel = 1;
                this.translateX = 0;
                this.translateY = 0;
                this.updateTransform();
            }
        }
        
        document.addEventListener('DOMContentLoaded', function() {
            new MapZoom();
        });
        
        setInterval(function() {
            location.reload();
        }, 30000);
    </script>
</body>
</html>
`

var tmpl = template.Must(template.New("map").Parse(mapTemplate))

func StartServer(rpc *rustplus.Client, port int) {
	http.HandleFunc("/map", func(w http.ResponseWriter, r *http.Request) {
		handleMap(w, r, rpc)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/map", http.StatusFound)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[web] Starting map server on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleMap(w http.ResponseWriter, r *http.Request, rpc *rustplus.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mapData *rustplus.AppMap
	done := make(chan bool)

	err := rpc.GetMap(ctx, func(msg *rustplus.AppMessage) bool {
		if msg.GetResponse().GetMap() != nil {
			mapData = msg.GetResponse().GetMap()

			log.Println("[web] Received map data", "monuments:", len(mapData.Monuments), "size:", *mapData.Width, "x", *mapData.Height)
			log.Println("[web] Map data:", mapData.Monuments)
			log.Println("[web] Map data background:", *mapData.Background)

			select {
			case done <- true:
			default:
			}
			return true
		}
		return false
	})

	if err != nil {
		http.Error(w, "Failed to get map: "+err.Error(), http.StatusInternalServerError)
		return
	}

	select {
	case <-done:
	case <-ctx.Done():
		http.Error(w, "Timeout waiting for map data", http.StatusGatewayTimeout)
		return
	}

	if mapData == nil {
		http.Error(w, "No map data received", http.StatusInternalServerError)
		return
	}

	serverInfo := rpc.GetServerInfo()
	mapSize := 4000
	if serverInfo != nil {
		mapSize = int(*serverInfo.MapSize)
	}

	// Prepare data for template
	imageBase64 := base64.StdEncoding.EncodeToString(mapData.JpgImage)

	log.Printf("[web] Map size: %dx%d, monuments: %d", *mapData.Width, *mapData.Height, len(mapData.Monuments))

	monuments := make([]MonumentData, 0, len(mapData.Monuments))
	for _, m := range mapData.Monuments {
		if *m.Name == "train_tunnel_display_name" {
			continue
		}
		// Normalize coordinates using approximate map size (6000x6000 pixels)
		x, y := calculateRelativePosition(mapSize, float64(*m.X), float64(*m.Y))

		monuments = append(monuments, MonumentData{
			Name: *m.Name,
			X:    x,
			Y:    y,
		})
		log.Printf("[web] Monument: %s at %.1f%%, %.1f%% (pixels: %.0f, %.0f)", *m.Name, x, y, *m.X, *m.Y)
	}

	data := MapData{
		ImageBase64: imageBase64,
		Monuments:   monuments,
		Width:       *mapData.Width,
		Height:      *mapData.Height,
		CellsCount:  mapSize / 100,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[web] Template error: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func calculateRelativePosition(mapSize int, x, y float64) (float64, float64) {
	cellCount := mapSize / 100
	cellSize := float64(mapSize) / float64(cellCount-14)

	gridX := float64(x)/cellSize + 7
	gridY := float64(y)/cellSize + 7

	x = gridX / float64(cellCount) * 100
	y = 100 - gridY/float64(cellCount)*100

	return x, y
}
