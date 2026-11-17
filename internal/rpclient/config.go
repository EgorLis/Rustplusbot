package rpclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewFromConfig connects through the authenticated companion socket.
// Tokens are read from disk; they must never be included in diagnostics.
func NewFromConfig(cfg RustPlusConfig) (*RustPlus, error) {
	if strings.TrimSpace(cfg.ServerID) == "" {
		return nil, &PermanentError{"server_id is required; the old IP/port connection is no longer supported by the bot"}
	}

	rp := New("", 0, cfg.PlayerID, cfg.PlayerToken, false)
	rp.serverID = strings.TrimSpace(cfg.ServerID)
	rp.authToken = strings.TrimSpace(cfg.AuthToken)

	if rp.serverID != "" && rp.authToken == "" {
		tokenPath := cfg.AuthTokenFile
		if tokenPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("find Rust+ token file: %w", err)
			}

			tokenPath = filepath.Join(home, "rustplus.config.json")
		}

		rp.authTokenFile = tokenPath
		if err := rp.loadAuthToken(); err != nil {
			return nil, err
		}
	}

	if err := rp.validate(); err != nil {
		return nil, err
	}

	return rp, nil
}

// Re-read on each dial so renewing the saved login lets reconnect recover without restarting.
func (rp *RustPlus) loadAuthToken() error {
	if rp.authTokenFile == "" {
		return nil
	}

	data, err := os.ReadFile(rp.authTokenFile)
	if err != nil {
		return &PermanentError{fmt.Sprintf("read Rust+ auth_token_file: %v", err)}
	}

	var saved struct {
		Token string `json:"rustplus_auth_token"`
	}

	if json.Unmarshal(data, &saved) != nil {
		return &PermanentError{"invalid JSON in Rust+ auth_token_file"}
	}

	rp.authToken = strings.TrimSpace(saved.Token)

	return nil
}
