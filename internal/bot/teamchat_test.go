package bot

import (
	"testing"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
	"google.golang.org/protobuf/proto"
)

func TestChatHistoryDoesNotReplayCommandsOrDuplicateBroadcasts(t *testing.T) {
	b := New()
	message := func(text string, timestamp uint32) *rpclient.AppTeamMessage {
		return &rpclient.AppTeamMessage{SteamId: proto.Uint64(1), Time: proto.Uint32(timestamp), Message: proto.String(text)}
	}
	old := message("!track add 123 old", 10)
	b.receiveChat(old, true)
	b.receiveChat(old, false)
	if len(b.chatQueue) != 0 {
		t.Fatal("replayed a historical command")
	}
	newMessage := message("!help", 11)
	b.receiveChat(newMessage, false)
	b.receiveChat(proto.Clone(newMessage).(*rpclient.AppTeamMessage), false)
	if len(b.chatQueue) != 1 {
		t.Fatalf("expected one command across history and broadcast, got %d", len(b.chatQueue))
	}
	b.receiveChat(message("!help", 12), false)
	if len(b.chatQueue) != 2 {
		t.Fatal("a later repeated command was lost")
	}
}
