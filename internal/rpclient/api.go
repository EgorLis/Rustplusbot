package rpclient

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/protobuf/proto"
)

// ========================= high-level API  =========================

func (rp *RustPlus) SetEntityValue(entityID uint64, value bool, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		EntityId: proto.Uint64(entityID),
		SetEntityValue: &AppSetEntityValue{
			Value: proto.Bool(value),
		},
	}, cb)
}

func (rp *RustPlus) TurnSmartSwitchOn(entityID uint64, cb func(*AppMessage) bool) error {
	return rp.SetEntityValue(entityID, true, cb)
}

func (rp *RustPlus) TurnSmartSwitchOff(entityID uint64, cb func(*AppMessage) bool) error {
	return rp.SetEntityValue(entityID, false, cb)
}

// Strobe — как в JS: быстрое мигание (осторожно с rate limit).
func (rp *RustPlus) Strobe(ctx context.Context, entityID uint64, interval time.Duration, start bool) {
	_ = rp.SetEntityValue(entityID, start, nil)
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
				if err := rp.SetEntityValue(entityID, val, nil); err != nil {
					log.Println("strobe:", err)
				}
			}
		}
	}()
}

func (rp *RustPlus) SendTeamMessage(message string, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		SendTeamMessage: &AppSendMessage{
			Message: proto.String(message),
		},
	}, cb)
}

func (rp *RustPlus) GetEntityInfo(entityID uint64, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		EntityId:      proto.Uint64(entityID),
		GetEntityInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetMap(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetMap: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetTime(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetTime: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetMapMarkers(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetMapMarkers: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetInfo(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) GetTeamInfo(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		GetTeamInfo: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) SubscribeToCamera(identifier string, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraSubscribe: &AppCameraSubscribe{
			CameraId: proto.String(identifier),
		},
	}, cb)
}

func (rp *RustPlus) UnsubscribeFromCamera(cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraUnsubscribe: &AppEmpty{},
	}, cb)
}

func (rp *RustPlus) SendCameraInput(buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return rp.SendRequest(&AppRequest{
		CameraInput: &AppCameraInput{
			Buttons:    proto.Int32(buttons),
			MouseDelta: &Vector2{X: &dx, Y: &dy},
		},
	}, cb)
}

func (rp *RustPlus) BotSay(msg string) error {
	text := "[bot] " + msg
	responses := make(chan *AppResponse, 1)
	request := &AppRequest{SendTeamMessage: &AppSendMessage{Message: proto.String(text)}}
	err := rp.SendRequest(request, func(message *AppMessage) bool {
		responses <- message.GetResponse()
		return true
	})
	if err != nil {
		rp.setTeamChatError(err)
		log.Printf("Team chat: message not sent: %v", err)
		return err
	}

	// Never wait on the socket reader: alarm callbacks can send messages too.
	go func() {
		defer rp.removeCallback(request.GetSeq())
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		var deliveryError error
		select {
		case response := <-responses:
			if response == nil {
				deliveryError = fmt.Errorf("empty response")
			} else if response.Error != nil {
				deliveryError = fmt.Errorf("%s", response.Error.GetError())
			}
		case <-timer.C:
			deliveryError = fmt.Errorf("server did not confirm delivery within 8 seconds")
		}
		rp.setTeamChatError(deliveryError)
		if deliveryError != nil {
			log.Printf("Team chat: delivery not confirmed: %v; text=%q", deliveryError, text)
			return
		}
		log.Println(text)
	}()
	return nil
}

// ========================= удобный враппер Camera =========================

type Camera struct {
	rp  *RustPlus
	id  string
	sub bool
}

func (rp *RustPlus) GetCamera(identifier string) *Camera {
	return &Camera{rp: rp, id: identifier}
}

func (c *Camera) Subscribe(cb func(*AppMessage) bool) error {
	if err := c.rp.SubscribeToCamera(c.id, cb); err != nil {
		return err
	}

	c.sub = true
	return nil
}

func (c *Camera) Unsubscribe(cb func(*AppMessage) bool) error {
	if !c.sub {
		return nil
	}

	c.sub = false
	return c.rp.UnsubscribeFromCamera(cb)
}

func (c *Camera) Input(buttons int32, dx, dy float32, cb func(*AppMessage) bool) error {
	return c.rp.SendCameraInput(buttons, dx, dy, cb)
}
