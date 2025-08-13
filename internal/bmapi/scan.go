package bmapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

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
// string:name, true — игрок на сервере, false — оффлайн.
func (c *Client) IsOnline() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]bool, len(c.playersToDetect))
	for id, name := range c.playersToDetect {
		_, online := c.lastPlayersScan[id]
		status[name] = online
	}
	return status
}

func (c *Client) FormatOnlineInfo(players map[string]bool) string {
	var online, offline []string
	for name, isOnline := range players {
		if isOnline {
			online = append(online, name)
		} else {
			offline = append(offline, name)
		}
	}
	return fmt.Sprintf("Онлайн: %s | Оффлайн: %s",
		strings.Join(online, ", "), strings.Join(offline, ", "))
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

	if len(br.Included) == 0 {
		log.Println("[online]-> сервер пустой")
	}

	for _, inc := range br.Included {
		if inc.Type == "player" {
			names[inc.ID] = inc.Attributes.Name
			log.Printf("[online]-> id %s: name %s", inc.ID, inc.Attributes.Name)
		}
	}

	log.Println("[online] ======================================")

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
