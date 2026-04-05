package bmapi

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/tools"
)

var logger = log.New(os.Stdout, "[bmapi] ", log.LstdFlags)

type Client struct {
	http   *http.Client
	token  string
	server string

	mu              sync.RWMutex
	playersToDetect map[string]string // кого отслеживаем (id->name, name может быть пустым)
	lastPlayersScan map[string]string // последний снимок (только отслеживаемые)
	running         bool
	stopCh          chan struct{}

	etag string // для If-None-Match
}

type BMConf struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// Создает новый клиент BM Api и возрващает его (задаем все параметры)
func NewClient(token, server string, players ...Player) *Client {
	watch := make(map[string]string, len(players))
	for _, p := range players {
		watch[p.ID] = p.Name
	}
	return &Client{
		http:            &http.Client{Timeout: 10 * time.Second},
		token:           token,
		server:          server,
		playersToDetect: watch,
		lastPlayersScan: map[string]string{},
	}
}

// Создает новый клиент BM Api и возрващает его (задаем через файл конфигурации)
func NewClientFromConf(conf BMConf) *Client {
	watch := make(map[string]string)

	return &Client{
		http:            &http.Client{Timeout: 10 * time.Second},
		token:           conf.Token,
		server:          conf.Server,
		playersToDetect: watch,
		lastPlayersScan: map[string]string{},
	}
}

func (c *Client) UseCircularBufferLogs(circularBufferLogs *tools.CircularBuffer) {
	logger = log.New(circularBufferLogs, "[bmapi] ", log.LstdFlags)
}

// AddPlayer добавляет нового игрока для отслеживания
func (c *Client) AddPlayer(players ...Player) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range players {
		c.playersToDetect[p.ID] = p.Name
	}
}

// RemovePlayer удаляет игрока с данным playerId из списка отслеживаемых
func (c *Client) RemovePlayer(playerId string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.playersToDetect, playerId)
}

// OnlineTrackedPlayers возвращает список отслеживаемых игроков в сети на момент последнего скана
func (c *Client) OnlineTrackedPlayers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make(map[string]string, len(c.lastPlayersScan))
	maps.Copy(cp, c.lastPlayersScan)
	return cp
}

func (c *Client) AllTrackedPlayers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make(map[string]string, len(c.playersToDetect))
	maps.Copy(cp, c.playersToDetect)
	return cp
}

// GetAllPlayers возвращает всех игроков на сервере (id -> name)
func (c *Client) GetAllPlayers() (map[string]string, error) {
	req, _ := http.NewRequest("GET",
		fmt.Sprintf("https://api.battlemetrics.com/servers/%s?include=player", c.server), nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bm api status %d", resp.StatusCode)
	}

	var br BMResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return nil, err
	}

	names := make(map[string]string)
	for _, inc := range br.Included {
		if inc.Type == "player" {
			names[inc.ID] = inc.Attributes.Name
		}
	}
	return names, nil
}
