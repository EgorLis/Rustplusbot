//Пакет для взаимодействия с API Battle Metrics

package bmapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

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
	Server  string   `json:"server"`
	Token   string   `json:"token"`
	Players []Player `json:"players"`
}

type Player struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

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

func NewClientFromConf(conf BMConf) *Client {
	watch := make(map[string]string, len(conf.Players))
	for _, p := range conf.Players {
		watch[p.ID] = p.Name
	}
	return &Client{
		http:            &http.Client{Timeout: 10 * time.Second},
		token:           conf.Token,
		server:          conf.Server,
		playersToDetect: watch,
		lastPlayersScan: map[string]string{},
	}
}

func (c *Client) AddPlayer(players ...Player) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range players {
		c.playersToDetect[p.ID] = p.Name
	}
}

func (c *Client) Players() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make(map[string]string, len(c.lastPlayersScan))
	for k, v := range c.lastPlayersScan {
		cp[k] = v
	}
	return cp
}

// StartScan запускает фоновый опрос и уведомления.
// notify вызывается строкой-сообщением (сделай там отправку в чат Rust+).
func (c *Client) StartScan(interval time.Duration, notify func(string)) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = true
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	// стартовая инициализация — без уведомлений
	if cur, err := c.fetchPlayers(); err == nil {
		c.mu.Lock()
		c.lastPlayersScan = cur
		c.mu.Unlock()
	}

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				cur, err := c.fetchPlayers()
				if err != nil {
					//notify(fmt.Sprintf("BM error: %v", err))
					log.Println(err)
					continue
				}
				c.mu.Lock()
				prev := c.lastPlayersScan
				// join
				for id, name := range cur {
					if _, ok := prev[id]; !ok {
						if wantName, track := c.playersToDetect[id]; track {
							// если имени не было — подменим пустое
							if wantName == "" {
								wantName = name
							}
							notify(fmt.Sprintf("➡ %s вошёл на сервер", wantName))
						}
					}
				}
				// leave
				for id, name := range prev {
					if _, ok := cur[id]; !ok {
						wantName := c.playersToDetect[id]
						if wantName == "" {
							wantName = name
						}
						if _, track := c.playersToDetect[id]; track {
							notify(fmt.Sprintf("⬅ %s покинул сервер", wantName))
						}
					}
				}
				// обновляем снимок
				c.lastPlayersScan = cur
				c.mu.Unlock()

			case <-c.stopCh:
				return
			}
		}
	}()
	return nil
}

func (c *Client) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	close(c.stopCh)
	c.running = false
	c.mu.Unlock()
}

// IsOnline возвращает текущее состояние для всех отслеживаемых игроков.
// true — игрок на сервере, false — оффлайн.
func (c *Client) IsOnline() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]bool, len(c.playersToDetect))
	for id := range c.playersToDetect {
		_, online := c.lastPlayersScan[id]
		status[id] = online
	}
	return status
}

// fetchPlayers — получает текущих игроков, фильтруя только отслеживаемых.
// Возвращает map[id]name только по тем, кто есть и в онлайне, и в watch-листе.
// Использует ETag для экономии.
func (c *Client) fetchPlayers() (map[string]string, error) {
	req, _ := http.NewRequest("GET",
		fmt.Sprintf("https://api.battlemetrics.com/servers/%s?include=player", c.server), nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	c.mu.RLock()
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	c.mu.RUnlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 304 — ничего не изменилось, отдадим предыдущий снимок
	if resp.StatusCode == http.StatusNotModified {
		c.mu.RLock()
		prev := make(map[string]string, len(c.lastPlayersScan))
		for k, v := range c.lastPlayersScan {
			prev[k] = v
		}
		c.mu.RUnlock()
		log.Println("304 — ничего не изменилось")
		return prev, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, errors.New("bm api non-200")
	}

	if et := resp.Header.Get("ETag"); et != "" {
		c.mu.Lock()
		c.etag = et
		c.mu.Unlock()
	}

	var br BMResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return nil, err
	}

	// id->name текущие игроки сервера
	names := map[string]string{}
	for _, inc := range br.Included {
		if inc.Type == "player" {
			names[inc.ID] = inc.Attributes.Name
			log.Printf("[online]-> id %s: name %s", inc.ID, inc.Attributes.Name)
		}
	}

	// фильтруем только отслеживаемых
	c.mu.RLock()
	watch := make(map[string]string, len(c.playersToDetect))
	for id, name := range c.playersToDetect {
		watch[id] = name
	}
	c.mu.RUnlock()

	curTracked := make(map[string]string)
	for id, realName := range names {
		if _, track := watch[id]; track {
			curTracked[id] = realName
		}
	}
	return curTracked, nil
}
