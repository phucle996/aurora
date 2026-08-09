package security

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"testing"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

type zonePayloadKeyResolverStub struct {
	result *hierarchyEntity.ResolveZonePayloadKey
	err    error
	input  *hierarchyEntity.ResolveZonePayloadKey
}

func (s *zonePayloadKeyResolverStub) ResolveZonePayloadKey(_ context.Context, in *hierarchyEntity.ResolveZonePayloadKey) (*hierarchyEntity.ResolveZonePayloadKey, error) {
	s.input = in
	return s.result, s.err
}

func TestProtectedJobPayloadV1Vector(t *testing.T) {
	const protectedPayloadBase64 = "CAESEKqqqqqqqkqqiqqqqqqqqqoaEAAAAAAAAAAAAAAAAAAAAAEgASogBLzS4NAPLM5f6PHGwvvsXAf6VuOqXIilaJl12Is/zgUyK7GMLw1w6CR7+HyXe9bjappULQjr7EbVyGKtb96k8BxWGbpkq3l6nSneaQ04Gw=="

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
	if actual := base64.StdEncoding.EncodeToString(protected.Payload); actual != protectedPayloadBase64 {
		t.Fatalf("protected payload wire drifted:\nwant %s\n got %s", protectedPayloadBase64, actual)
	}
}

func TestProtectorResolvesZoneKeyWithoutDatabaseOwnership(t *testing.T) {
	zoneID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	keyID := uuid.MustParse("10000000-0000-7000-8000-000000000001")
	privateKey, err := ecdh.X25519().GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x33}, 32)))
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	resolver := &zonePayloadKeyResolverStub{result: &hierarchyEntity.ResolveZonePayloadKey{
		ZoneID: zoneID, KeyID: keyID, PublicKey: privateKey.PublicKey().Bytes(), Available: true,
	}}
	protector, err := NewProtector(resolver)
	if err != nil {
		t.Fatalf("construct protector: %v", err)
	}
	protected, err := protector.Seal(context.Background(), Metadata{
		ZoneID: zoneID, SourceDomain: "STORAGE", JobTopic: "storage.bucket.create",
		ResourceID: "bucket-1", JobVersion: 1, PayloadSchemaVersion: 1,
	}, []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if resolver.input == nil || resolver.input.ZoneID != zoneID || protected.KeyID != keyID {
		t.Fatalf("resolver boundary mismatch: input=%+v key=%s", resolver.input, protected.KeyID)
	}
}

func TestProtectorFailsClosedWhenHierarchyKeyIsUnavailable(t *testing.T) {
	resolver := &zonePayloadKeyResolverStub{result: &hierarchyEntity.ResolveZonePayloadKey{}}
	protector, err := NewProtector(resolver)
	if err != nil {
		t.Fatalf("construct protector: %v", err)
	}
	_, err = protector.Seal(context.Background(), Metadata{ZoneID: uuid.New()}, []byte("payload"))
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("expected ErrKeyUnavailable, got %v", err)
	}
}
