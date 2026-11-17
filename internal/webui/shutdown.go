package webui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// StopLocal identifies the local panel before asking it to stop its bot and exit.
// It never terminates a process by name or trusts a saved PID.
func StopLocal(parent context.Context, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("UI port must be between 1 and 65535")
	}

	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	address := fmt.Sprintf("127.0.0.1:%d", port)
	base := "http://" + address
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/identity", nil)
	if err != nil {
		return err
	}

	response, err := client.Do(request)
	if err != nil {
		var connection *net.OpError
		if errors.As(err, &connection) && connection.Op == "dial" && !connection.Timeout() {
			fmt.Println("Web panel is not running.")
			return nil
		}

		return fmt.Errorf("contact local panel: %w", err)
	}
	response.Body.Close()

	if response.StatusCode != http.StatusOK || response.Header.Get("X-RustPlusBot-UI") != "1" {
		return errors.New("this port is not serving a compatible RustPlusBot panel; no shutdown request was sent")
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/shutdown", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err = client.Do(request)
	if err != nil {
		return fmt.Errorf("stop local panel: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("panel could not finish shutdown (HTTP %d); it is still available", response.StatusCode)
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return fmt.Errorf("read panel shutdown response: %w", err)
	}

	// Wait for the listener to disappear so the script can safely be followed by Start-WebUI.cmd.
	for {
		connection, err := net.DialTimeout("tcp4", address, 200*time.Millisecond)
		if err != nil {
			fmt.Println("Bot and web panel stopped.")
			return nil
		}
		connection.Close()

		select {
		case <-ctx.Done():
			return errors.New("shutdown was requested, but the panel has not released its port yet")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
