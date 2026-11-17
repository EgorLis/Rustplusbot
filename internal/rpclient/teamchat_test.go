package rpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

func TestTeamSendAcceptsEchoAndStillDeliversBroadcast(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request AppRequest
			if proto.Unmarshal(data, &request) != nil {
				return
			}
			if request.GetInfo != nil {
				_ = conn.WriteMessage(websocket.BinaryMessage, infoReply(request.GetSeq()))
				continue
			}
			message := &AppMessage{Broadcast: &AppBroadcast{TeamMessage: &AppNewTeamMessage{Message: &AppTeamMessage{SteamId: proto.Uint64(1), Message: proto.String(request.GetSendTeamMessage().GetMessage())}}}}
			data, _ = (proto.MarshalOptions{AllowPartial: true}).Marshal(message)
			_ = conn.WriteMessage(websocket.BinaryMessage, data)
		}
	}))
	defer server.Close()
	client := testClient(server)
	broadcast := make(chan struct{}, 1)
	client.OnMessage = func(message *AppMessage) {
		if message.Broadcast != nil {
			broadcast <- struct{}{}
		}
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	response, err := client.SendRequestAsync(&AppRequest{SendTeamMessage: &AppSendMessage{Message: proto.String("test")}}, time.Second)
	if err != nil || response.GetSuccess() == nil {
		t.Fatalf("echo did not confirm delivery: %v", err)
	}
	await(t, broadcast, "chat broadcast after acknowledgement")
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.cbs) != 0 || len(client.teamSends) != 0 {
		t.Fatal("pending chat callback leaked")
	}
}
