package rpclient

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestEntityIDsAboveUint32SurviveWireRoundTrip(t *testing.T) {
	const entityID uint64 = 1<<40 + 123

	request := &AppRequest{
		Seq:           proto.Uint32(1),
		PlayerId:      proto.Uint64(1),
		PlayerToken:   proto.Int32(-1),
		EntityId:      proto.Uint64(entityID),
		GetEntityInfo: &AppEmpty{},
	}

	data, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	var decodedRequest AppRequest
	if err := proto.Unmarshal(data, &decodedRequest); err != nil {
		t.Fatal(err)
	}

	if decodedRequest.GetEntityId() != entityID {
		t.Fatalf("request ID truncated: %d", decodedRequest.GetEntityId())
	}

	message := &AppMessage{
		Broadcast: &AppBroadcast{
			EntityChanged: &AppEntityChanged{
				EntityId: proto.Uint64(entityID),
				Payload:  &AppEntityPayload{Value: proto.Bool(true)},
			},
		},
	}

	data, err = proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}

	var decodedMessage AppMessage
	if err := proto.Unmarshal(data, &decodedMessage); err != nil {
		t.Fatal(err)
	}

	if decodedMessage.GetBroadcast().GetEntityChanged().GetEntityId() != entityID {
		t.Fatal("broadcast entity ID was truncated")
	}
}
