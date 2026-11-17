package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type BMMatch struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Status string `json:"status"`
	Exact  bool   `json:"exact"`
}

func searchBattleMetrics(ctx context.Context, name, token string) ([]BMMatch, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("для поиска нужен сохранённый API-токен BattleMetrics в настройках")
	}

	query := url.Values{"filter[game]": {"rust"}, "filter[search]": {name}, "page[size]": {"100"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.battlemetrics.com/servers?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("не удалось связаться с BattleMetrics")
	}

	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BattleMetrics HTTP %d; проверьте API-токен", response.StatusCode)
	}

	var result struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name   string `json:"name"`
				IP     string `json:"ip"`
				Port   int    `json:"port"`
				Status string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}

	if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result) != nil {
		return nil, errors.New("некорректный ответ BattleMetrics")
	}

	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	matches := []BMMatch{}
	for _, server := range result.Data {
		exact := normalize(server.Attributes.Name) == normalize(name)
		if !exact {
			continue
		}

		matches = append(matches, BMMatch{ID: server.ID, Name: server.Attributes.Name, IP: server.Attributes.IP, Port: server.Attributes.Port, Status: server.Attributes.Status, Exact: true})
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].Status == "online" && matches[j].Status != "online" })
	return matches, nil
}
