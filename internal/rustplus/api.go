package rustplus

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

// ========================= high-level API  =========================

func (c *Client) SetEntityValue(ctx context.Context, entityID uint32, value bool, cb func(*AppMessage) bool) error {
	return c.addTask(ctx,
		task{
			Request: &AppRequest{EntityId: proto.Uint32(entityID)},
			Cb:      cb,
		},
	)
}

func (c *Client) TurnSmartSwitchOn(ctx context.Context, entityID uint32, cb func(*AppMessage) bool) error {
	return c.SetEntityValue(ctx, entityID, true, cb)
}

func (c *Client) TurnSmartSwitchOff(ctx context.Context, entityID uint32, cb func(*AppMessage) bool) error {
	return c.SetEntityValue(ctx, entityID, false, cb)
}

// Strobe — как в JS: быстрое мигание (осторожно с rate limit).
func (c *Client) Strobe(ctx context.Context, entityID uint32, interval time.Duration, start bool) {
	_ = c.SetEntityValue(ctx, entityID, start, nil)
	t := time.NewTicker(interval)
	go func() {
		defer t.Stop()
		val := start
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				val = !val
				if err := c.SetEntityValue(ctx, entityID, val, nil); err != nil {
					logger.Println("strobe:", err)
				}
			}
		}
	}()
}

func (c *Client) SendTeamMessage(ctx context.Context, message string, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			SendTeamMessage: &AppSendMessage{
				Message: proto.String(message),
			},
		},
		Cb: cb,
	})
}

func (c *Client) GetEntityInfo(ctx context.Context, entityID uint32, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			EntityId:      proto.Uint32(entityID),
			GetEntityInfo: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) GetMap(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			GetMap: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) GetTime(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			GetTime: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) GetMapMarkers(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			GetMapMarkers: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) GetInfo(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			GetInfo: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) GetTeamInfo(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			GetTeamInfo: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) SubscribeToCamera(ctx context.Context, identifier string, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			CameraSubscribe: &AppCameraSubscribe{
				CameraId: proto.String(identifier),
			},
		},
		Cb: cb,
	})
}

func (c *Client) UnsubscribeFromCamera(ctx context.Context, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			CameraUnsubscribe: &AppEmpty{},
		},
		Cb: cb,
	})
}

func (c *Client) SendCameraInput(ctx context.Context, buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return c.addTask(ctx, task{
		Request: &AppRequest{
			CameraInput: &AppCameraInput{
				Buttons:    proto.Int32(buttons),
				MouseDelta: &Vector2{X: &dx, Y: &dy},
			},
		},
		Cb: cb,
	})
}

func (c *Client) BotSay(ctx context.Context, msg string) error {
	text := "[bot] " + msg
	err := c.SendTeamMessage(ctx, text, nil)
	logger.Println(text)
	if err != nil {
		logger.Println(err)
	}
	return err
}

// ========================= удобный враппер Camera =========================

type Camera struct {
	rp  *Client
	id  string
	sub bool
}

func (c *Client) GetCamera(identifier string) *Camera {
	return &Camera{rp: c, id: identifier}
}

func (c *Camera) Subscribe(ctx context.Context, cb func(*AppMessage) bool) error {
	if err := c.rp.SubscribeToCamera(ctx, c.id, cb); err != nil {
		return err
	}
	c.sub = true
	return nil
}

func (c *Camera) Unsubscribe(ctx context.Context, cb func(*AppMessage) bool) error {
	if !c.sub {
		return nil
	}
	c.sub = false
	return c.rp.UnsubscribeFromCamera(ctx, cb)
}

func (c *Camera) Input(ctx context.Context, buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return c.rp.SendCameraInput(ctx, buttons, dx, dy, cb)
}
