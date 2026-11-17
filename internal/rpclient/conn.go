package rpclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

// PermanentError indicates configuration or credentials that retrying cannot fix.
type PermanentError struct {
	Message string
}

func (e *PermanentError) Error() string {
	return e.Message
}

func (rp *RustPlus) wsURL() string {
	if rp.serverID != "" {
		return "wss://companion-rust.facepunch.com/socket/" + rp.serverID
	}

	return "ws://" + net.JoinHostPort(rp.server, strconv.Itoa(rp.port))
}

func (rp *RustPlus) validate() error {
	if rp.playerID == 0 {
		return &PermanentError{"player_id is missing in conf/rpconfig.json"}
	}

	if rp.serverID != "" {
		if len(rp.serverID) != 36 || strings.ContainsAny(rp.serverID, "/?# \r\n") {
			return &PermanentError{"invalid server_id; use the UUID from the Rust+ pairing notification"}
		}

		if strings.TrimSpace(rp.authToken) == "" {
			return &PermanentError{"companion auth token is missing; set auth_token_file or auth_token"}
		}
	} else {
		if rp.useProxy {
			return &PermanentError{"the old Facepunch /game proxy is retired; configure server_id and auth_token_file"}
		}

		if strings.TrimSpace(rp.server) == "" || rp.port < 1 || rp.port > 65535 {
			return &PermanentError{"invalid server or Rust+ app.port in conf/rpconfig.json"}
		}
	}

	return nil
}

func (rp *RustPlus) dialAndSetup(ctx context.Context) (*websocket.Conn, error) {
	if err := rp.loadAuthToken(); err != nil {
		return nil, err
	}

	if err := rp.validate(); err != nil {
		return nil, err
	}

	attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	if rp.serverID != "" {
		dialer.Subprotocols = []string{"rustplus.auth.v1", base64.RawURLEncoding.EncodeToString([]byte(rp.authToken))}
	}

	conn, response, err := dialer.DialContext(attempt, rp.wsURL(), nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}

	if err != nil {
		if attempt.Err() != nil {
			return nil, attempt.Err()
		}

		if response != nil {
			switch response.StatusCode {
			case http.StatusForbidden, http.StatusUnauthorized:
				return nil, &PermanentError{"Rust+ rejected the companion auth token; sign in to Rust+ again and update auth_token_file"}
			case http.StatusNotFound:
				if rp.serverID != "" {
					return nil, &PermanentError{"Rust+ server_id was not found; check the pairing notification"}
				}
			case http.StatusTeapot:
				return nil, &PermanentError{"the old Facepunch proxy is retired; use server_id and companion authentication"}
			}

			return nil, fmt.Errorf("Rust+ WebSocket handshake: HTTP %d", response.StatusCode)
		}

		return nil, fmt.Errorf("Rust+ WebSocket connection: %w", err)
	}

	conn.SetReadLimit(64 << 20)
	// Cancel a stalled authentication read immediately, including a short startup budget.
	closeOnCancel := context.AfterFunc(attempt, func() {
		conn.Close()
	})

	err = rp.probe(attempt, conn)
	stopped := closeOnCancel()
	if err != nil || !stopped || attempt.Err() != nil {
		conn.Close()
		if attempt.Err() != nil {
			return nil, attempt.Err()
		}

		return nil, err
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	return conn, nil
}

func (rp *RustPlus) probe(ctx context.Context, conn *websocket.Conn) error {
	seq := rp.nextSeq()
	request := &AppRequest{
		Seq:         &seq,
		PlayerId:    &rp.playerID,
		PlayerToken: &rp.playerToken,
		GetInfo:     &AppEmpty{},
	}

	data, err := proto.Marshal(request)
	if err != nil {
		return err
	}

	deadline, _ := ctx.Deadline()
	conn.SetWriteDeadline(deadline)
	conn.SetReadDeadline(deadline)
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		return err
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("Rust+ server verification: %w", err)
		}

		var msg AppMessage
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("invalid Rust+ response: %w", err)
		}

		resp := msg.GetResponse()
		if resp == nil || resp.GetSeq() != seq {
			continue
		}

		if resp.Error != nil {
			return &PermanentError{fmt.Sprintf("Rust+ rejected GetInfo (%s); check player_id/player_token and pair with the server again", resp.Error.GetError())}
		}

		if resp.Info == nil || resp.Info.GetName() == "" {
			return fmt.Errorf("Rust+ GetInfo returned no server information")
		}

		rp.connMu.Lock()
		rp.serverInfo = resp.Info
		rp.connMu.Unlock()

		return nil
	}
}

func (rp *RustPlus) closeConn() {
	rp.connMu.Lock()
	conn := rp.conn
	rp.conn = nil
	rp.connMu.Unlock()

	if conn != nil {
		conn.Close()
	}
}

// Each heartbeat owns its socket; an old goroutine can never close a new connection.
func (rp *RustPlus) startHeartbeat(ctx context.Context, conn *websocket.Conn) {
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}

				if err := rp.sendRequestOn(conn, &AppRequest{GetInfo: &AppEmpty{}}, nil); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
}
