package bot

import (
	"encoding/json"
	"testing"
)

func TestLargeDeviceIDInCommandsAndSavedConfig(t *testing.T) {
	const text = "1099511627899"
	const want uint64 = 1099511627899

	id, err := parseEntityID(text)
	if err != nil || id != want {
		t.Fatalf("large command ID: %d, %v", id, err)
	}

	data := []byte(`{"bt1":{"id":1099511627899,"name":"switch"},"alarms":{"1099511627899":{"id":1099511627899}}}`)
	var cfg BotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.BT1.ID != want || cfg.Alarms[want].ID != want {
		t.Fatal("large ID was lost while loading the config")
	}

	saved, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var restored BotConfig
	if err := json.Unmarshal(saved, &restored); err != nil {
		t.Fatal(err)
	}

	if restored.BT1.ID != want || restored.Alarms[want].ID != want {
		t.Fatal("large ID was lost while saving the config")
	}
}
