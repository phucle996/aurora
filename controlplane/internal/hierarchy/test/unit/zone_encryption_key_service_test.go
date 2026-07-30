package unit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	entity "controlplane/internal/hierarchy/domain/entity"
	serviceimpl "controlplane/internal/hierarchy/service"
	"controlplane/internal/hierarchy/test/mocks"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

func TestZoneEncryptionKeyServiceOwnsIdentityAndDerivedMetadata(t *testing.T) {
	repository := &mocks.ZoneEncryptionKeyRepository{}
	service := serviceimpl.NewZoneEncryptionKeyService(repository, observability.NewNoopWorkflowRecorder())
	publicKey := bytes.Repeat([]byte{0x21}, 32)
	in := &entity.RegisterZoneEncryptionKey{PublicKey: publicKey}

	if _, err := service.RegisterZoneEncryptionKey(context.Background(), in); err != nil {
		t.Fatalf("register key: %v", err)
	}
	if repository.Registered == nil || repository.Registered.ID == uuid.Nil {
		t.Fatal("service must generate key UUID before repository")
	}
	wantFingerprint := sha256.Sum256(publicKey)
	if !bytes.Equal(repository.Registered.Fingerprint, wantFingerprint[:]) {
		t.Fatal("service must derive SHA-256 fingerprint from exact public key bytes")
	}
	if repository.Registered.Algorithm != entity.ZoneEncryptionKeyAlgorithm {
		t.Fatalf("unexpected algorithm: %s", repository.Registered.Algorithm)
	}
	if repository.Registered.Status != entity.ZoneEncryptionKeyStatusStaged {
		t.Fatalf("unexpected initial status: %s", repository.Registered.Status)
	}
}

func TestZoneEncryptionKeyServicePreservesRetryIdentity(t *testing.T) {
	repository := &mocks.ZoneEncryptionKeyRepository{}
	service := serviceimpl.NewZoneEncryptionKeyService(repository, observability.NewNoopWorkflowRecorder())
	keyID := uuid.MustParse("10000000-0000-7000-8000-000000000001")
	in := &entity.RegisterZoneEncryptionKey{ID: keyID, PublicKey: bytes.Repeat([]byte{0x42}, 32)}

	if _, err := service.RegisterZoneEncryptionKey(context.Background(), in); err != nil {
		t.Fatalf("register key retry: %v", err)
	}
	if repository.Registered.ID != keyID {
		t.Fatal("service must preserve identity for an internal retry")
	}
}

func TestZoneEncryptionKeyServiceKeepsWorkflowsIsolated(t *testing.T) {
	repository := &mocks.ZoneEncryptionKeyRepository{}
	service := serviceimpl.NewZoneEncryptionKeyService(repository, observability.NewNoopWorkflowRecorder())
	zoneID := uuid.MustParse("10000000-0000-7000-8000-000000000010")
	keyID := uuid.MustParse("10000000-0000-7000-8000-000000000011")

	if _, err := service.ListZoneEncryptionKeys(context.Background(), &entity.ListZoneEncryptionKeys{ZoneID: zoneID}); err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if repository.Listed == nil || repository.Listed.ZoneID != zoneID {
		t.Fatal("list workflow must call only its repository method")
	}
	if _, err := service.ActivateZoneEncryptionKey(context.Background(), &entity.ActivateZoneEncryptionKey{ZoneID: zoneID, KeyID: keyID}); err != nil {
		t.Fatalf("activate key: %v", err)
	}
	if repository.Activated == nil || repository.Activated.KeyID != keyID {
		t.Fatal("activate workflow must call only its repository method")
	}
	if _, err := service.RetireZoneEncryptionKey(context.Background(), &entity.RetireZoneEncryptionKey{ZoneID: zoneID, KeyID: keyID}); err != nil {
		t.Fatalf("retire key: %v", err)
	}
	if repository.Retired == nil || repository.Retired.KeyID != keyID {
		t.Fatal("retire workflow must call only its repository method")
	}
}
