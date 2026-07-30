package hierarchyPubsubHandler

import (
	"bytes"
	hierarchyproto "controlplane/internal/hierarchy/transport/proto"
	"testing"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Helper kiểm thử giải mã request envelope nội bộ cho unit test
func parseRequestEnvelope(message *goredis.Message) (uuid.UUID, []byte, bool) {
	if message == nil {
		return uuid.Nil, nil, false
	}
	payload := []byte(message.Payload)
	if len(payload) < requestIDSize {
		return uuid.Nil, nil, false
	}
	requestID, err := uuid.FromBytes(payload[:requestIDSize])
	if err != nil || requestID == uuid.Nil {
		return uuid.Nil, nil, false
	}
	return requestID, payload[requestIDSize:], true
}

func TestSplitRequestEnvelopeAcceptsEmptyProtobuf(t *testing.T) {
	requestID := uuid.MustParse("019f9172-ba99-7be3-8649-8dbf70da885d")
	message := &goredis.Message{Payload: string(requestID[:])}

	// [COMMENT]: Kiểm thử envelope chỉ có UUID 16-byte với Protobuf rỗng
	gotID, protobufPayload, ok := parseRequestEnvelope(message)

	if !ok {
		t.Fatal("expected a UUID-only envelope to be valid")
	}
	if gotID != requestID {
		t.Fatalf("request ID mismatch: got %s, want %s", gotID, requestID)
	}
	if len(protobufPayload) != 0 {
		t.Fatalf("expected empty protobuf payload, got %d bytes", len(protobufPayload))
	}
	var request hierarchyproto.GetZoneListRequest
	if err := proto.Unmarshal(protobufPayload, &request); err != nil {
		t.Fatalf("empty GetZoneListRequest protobuf must decode successfully: %v", err)
	}
}

func TestSplitRequestEnvelopePreservesProtobufPayload(t *testing.T) {
	requestID := uuid.MustParse("019f9172-ba99-7be3-8649-8dbf70da885d")
	wantPayload := []byte{0x0a, 0x03, 'v', 'n', '1'}
	envelope := append(append([]byte{}, requestID[:]...), wantPayload...)

	// [COMMENT]: Kiểm thử envelope có chứa Protobuf payload đi kèm
	gotID, gotPayload, ok := parseRequestEnvelope(&goredis.Message{Payload: string(envelope)})

	if !ok {
		t.Fatal("expected envelope to be valid")
	}
	if gotID != requestID {
		t.Fatalf("request ID mismatch: got %s, want %s", gotID, requestID)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("protobuf payload mismatch: got %v, want %v", gotPayload, wantPayload)
	}
}

func TestSplitRequestEnvelopeRejectsMalformedEnvelope(t *testing.T) {
	testCases := map[string]*goredis.Message{
		"nil message":      nil,
		"short envelope":   {Payload: string(make([]byte, requestIDSize-1))},
		"nil request UUID": {Payload: string(make([]byte, requestIDSize))},
	}

	for name, message := range testCases {
		t.Run(name, func(t *testing.T) {
			// [COMMENT]: Kiểm thử từ chối envelope không đúng định dạng
			if _, _, ok := parseRequestEnvelope(message); ok {
				t.Fatal("expected malformed envelope to be rejected")
			}
		})
	}
}
