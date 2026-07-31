package security

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestProtectedJobPayloadV1Vector(t *testing.T) {
	var vector struct {
		ProtectedPayloadBase64 string `json:"protected_payload_base64"`
	}
	raw, err := os.ReadFile("../../../contracts/testdata/protected_payload_v1.json")
	if err != nil {
		t.Fatalf("read canonical protected-payload vector: %v", err)
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatalf("decode canonical protected-payload vector: %v", err)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("construct recipient private key: %v", err)
	}
	protected, err := seal(
		bytes.NewReader(bytes.Repeat([]byte{0x24}, 32)),
		bytes.Repeat([]byte{0x24}, 32),
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		privateKey.PublicKey().Bytes(),
		Metadata{
			ZoneID:               uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
			SourceDomain:         "STORAGE",
			JobTopic:             "storage.bucket.create",
			ResourceID:           "bucket-1",
			JobVersion:           1,
			PayloadSchemaVersion: 1,
		},
		[]byte("aurora-protected-payload-v1"),
	)
	if err != nil {
		t.Fatalf("seal deterministic vector: %v", err)
	}
	if actual := base64.StdEncoding.EncodeToString(protected.Payload); actual != vector.ProtectedPayloadBase64 {
		t.Fatalf("protected payload wire drifted:\nwant %s\n got %s", vector.ProtectedPayloadBase64, actual)
	}
}
