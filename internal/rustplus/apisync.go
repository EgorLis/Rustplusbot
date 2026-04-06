package rustplus

import (
	"context"
	"fmt"
)

// ========================= Synchronous wrappers =========================

// GetMapSync returns the map data synchronously
func (c *Client) GetMapSync(ctx context.Context) (*AppMap, error) {
	var mapData *AppMap
	done := make(chan error, 1)

	err := c.GetMap(ctx, func(msg *AppMessage) bool {
		if msg.GetResponse().GetMap() != nil {
			mapData = msg.GetResponse().GetMap()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no map data in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send map request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return mapData, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetInfoSync returns server info synchronously
func (c *Client) GetInfoSync(ctx context.Context) (*AppInfo, error) {
	var info *AppInfo
	done := make(chan error, 1)

	err := c.GetInfo(ctx, func(msg *AppMessage) bool {
		if msg.GetResponse().GetInfo() != nil {
			info = msg.GetResponse().GetInfo()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no info in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send info request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return info, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetTimeSync returns current server time synchronously
func (c *Client) GetTimeSync(ctx context.Context) (*AppTime, error) {
	var timeData *AppTime
	done := make(chan error, 1)

	err := c.GetTime(ctx, func(msg *AppMessage) bool {
		if msg.GetResponse().GetTime() != nil {
			timeData = msg.GetResponse().GetTime()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no time in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send time request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return timeData, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetMapMarkersSync returns map markers synchronously
func (c *Client) GetMapMarkersSync(ctx context.Context) (*AppMapMarkers, error) {
	var markers *AppMapMarkers
	done := make(chan error, 1)

	err := c.GetMapMarkers(ctx, func(msg *AppMessage) bool {
		if msg.GetResponse().GetMapMarkers() != nil {
			markers = msg.GetResponse().GetMapMarkers()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no map markers in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send map markers request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return markers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetTeamInfoSync returns team info synchronously
func (c *Client) GetTeamInfoSync(ctx context.Context) (*AppTeamInfo, error) {
	var teamInfo *AppTeamInfo
	done := make(chan error, 1)

	err := c.GetTeamInfo(ctx, func(msg *AppMessage) bool {
		if msg.GetResponse().GetTeamInfo() != nil {
			teamInfo = msg.GetResponse().GetTeamInfo()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no team info in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send team info request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return teamInfo, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetEntityInfoSync returns entity info synchronously
func (c *Client) GetEntityInfoSync(ctx context.Context, entityID uint32) (*AppEntityInfo, error) {
	var entityInfo *AppEntityInfo
	done := make(chan error, 1)

	err := c.GetEntityInfo(ctx, entityID, func(msg *AppMessage) bool {
		if msg.GetResponse().GetEntityInfo() != nil {
			entityInfo = msg.GetResponse().GetEntityInfo()
			done <- nil
			return true
		}
		done <- fmt.Errorf("no entity info in response")
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to send entity info request: %w", err)
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return entityInfo, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetEntityValueSync sets entity value and waits for confirmation
func (c *Client) SetEntityValueSync(ctx context.Context, entityID uint32, value bool) error {
	done := make(chan error, 1)

	err := c.SetEntityValue(ctx, entityID, value, func(msg *AppMessage) bool {
		// Check if we got a successful response
		if msg.GetResponse().GetEntityInfo() != nil {
			done <- nil
			return true
		}
		if msg.GetResponse().GetError() != nil {
			done <- fmt.Errorf("server error: %v", msg.GetResponse().GetError())
			return false
		}
		done <- fmt.Errorf("unexpected response")
		return false
	})

	if err != nil {
		return fmt.Errorf("failed to send entity value request: %w", err)
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TurnSmartSwitchOnSync turns on smart switch and waits for confirmation
func (c *Client) TurnSmartSwitchOnSync(ctx context.Context, entityID uint32) error {
	return c.SetEntityValueSync(ctx, entityID, true)
}

// TurnSmartSwitchOffSync turns off smart switch and waits for confirmation
func (c *Client) TurnSmartSwitchOffSync(ctx context.Context, entityID uint32) error {
	return c.SetEntityValueSync(ctx, entityID, false)
}

// Generic wrapper helper for simple requests that expect an empty response
func (c *Client) doSimpleRequest(ctx context.Context, requestName string, sendFunc func(context.Context, func(*AppMessage) bool) error) error {
	done := make(chan error, 1)

	err := sendFunc(ctx, func(msg *AppMessage) bool {
		// Check for successful response (no error)
		if msg.GetResponse().GetError() == nil {
			done <- nil
			return true
		}
		done <- fmt.Errorf("server error: %v", msg.GetResponse().GetError())
		return false
	})

	if err != nil {
		return fmt.Errorf("failed to send %s request: %w", requestName, err)
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
