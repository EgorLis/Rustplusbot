package webui

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Decode JSON using json.Number: Steam and entity IDs must never pass through float64.
func parsePairing(payload string) (map[string]string, error) {
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.UseNumber()
	if decoder.Decode(&raw) != nil {
		return nil, errors.New("вставьте JSON из поля body уведомления Rust+")
	}

	if appData, ok := raw["appData"].([]any); ok {
		for _, item := range appData {
			entry, _ := item.(map[string]any)
			if entry["key"] == "body" {
				if body, ok := entry["value"].(string); ok {
					return parsePairing(body)
				}
			}
		}
	}
	if body, ok := raw["body"].(string); ok {
		return parsePairing(body)
	}

	value := func(keys ...string) string {
		for _, key := range keys {
			switch v := raw[key].(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					return v
				}
			case json.Number:
				return v.String()
			}
		}
		return ""
	}

	kind := value("type")
	if kind == "server" {
		serverID, playerID, token := value("id", "serverId"), value("playerId"), value("playerToken")
		if !uuidPattern.MatchString(serverID) {
			return nil, errors.New("в уведомлении нет UUID сервера")
		}
		if _, err := positiveID(playerID); err != nil {
			return nil, errors.New("в уведомлении нет корректного playerId")
		}
		if token == "" {
			return nil, errors.New("в уведомлении нет playerToken")
		}
		return map[string]string{"kind": "server", "server_id": serverID, "player_id": playerID, "player_token": token, "name": value("name")}, nil
	}

	id := value("entityId", "entity_id")
	if _, err := positiveID(id); err != nil {
		return nil, errors.New("в уведомлении нет entityId; привяжите устройство к Rust+ в игре")
	}
	return map[string]string{"kind": "entity", "id": id, "name": value("entityName", "name"), "type": value("entityType", "entity_type"), "server_id": value("serverId", "server_id", "id")}, nil
}
