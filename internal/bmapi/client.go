package bmapi

import (
	"net/http"
	"sync"
	"time"
)

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

type Player struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type BMResponse struct {
	Data struct {
		ID            string `json:"id"`
		Relationships struct {
			Players struct {
				Data []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"players"`
		} `json:"relationships"`
	} `json:"data"`
	Included []struct {
		Type       string `json:"type"` // "player"
		ID         string `json:"id"`
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"included"`
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

// Players возвращает список отслеживаемых игроков в сети на момент последнего скана
func (c *Client) Players() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make(map[string]string, len(c.lastPlayersScan))
	for k, v := range c.lastPlayersScan {
		cp[k] = v
	}
	return cp
}
