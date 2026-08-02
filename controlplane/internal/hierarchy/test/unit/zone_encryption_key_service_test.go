package unit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	entity "controlplane/internal/hierarchy/domain/entity"
	serviceimpl "controlplane/internal/hierarchy/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/hierarchy/test/mocks"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

func TestZoneEncryptionKeyServiceOwnsIdentityAndDerivedMetadata(t *testing.T) {
	repository := &mocks.ZoneEncryptionKeyRepository{}
	service := serviceimpl.NewZoneEncryptionKeyService(repository, cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder()), &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())
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
	service := serviceimpl.NewZoneEncryptionKeyService(repository, cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder()), &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())
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
	service := serviceimpl.NewZoneEncryptionKeyService(repository, cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder()), &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())
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

func TestResolveZonePayloadKeyUsesL1AndReturnsDetachedMaterial(t *testing.T) {
	zoneID := uuid.MustParse("10000000-0000-7000-8000-000000000020")
	keyID := uuid.MustParse("10000000-0000-7000-8000-000000000021")
	repository := &mocks.ZoneEncryptionKeyRepository{ResolveResult: &entity.ResolveZonePayloadKey{
		ZoneID: zoneID, KeyID: keyID, PublicKey: bytes.Repeat([]byte{0x51}, 32), ReadyFor: time.Minute,
	}}
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	defer registry.L1.Close()
	service := serviceimpl.NewZoneEncryptionKeyService(repository, registry, &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())

	first, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID})
	if err != nil || !first.Available || first.KeyID != keyID {
		t.Fatalf("resolve first key: result=%+v err=%v", first, err)
	}
	first.PublicKey[0] = 0
	second, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID})
	if err != nil || !second.Available {
		t.Fatalf("resolve cached key: result=%+v err=%v", second, err)
	}
	if repository.ResolveCalls != 1 {
		t.Fatalf("expected one repository load behind L1, got %d", repository.ResolveCalls)
	}
	if second.PublicKey[0] != 0x51 {
		t.Fatal("caller mutation must not corrupt cached public key material")
	}
}

func TestActivateZoneEncryptionKeyPublishesL1InvalidationAfterCommit(t *testing.T) {
	zoneID := uuid.MustParse("10000000-0000-7000-8000-000000000025")
	keyID := uuid.MustParse("10000000-0000-7000-8000-000000000026")
	repository := &mocks.ZoneEncryptionKeyRepository{}
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	defer registry.L1.Close()
	fanout := &mocks.CacheFanout{}
	service := serviceimpl.NewZoneEncryptionKeyService(repository, registry, fanout, observability.NewNoopWorkflowRecorder())

	if _, err := service.ActivateZoneEncryptionKey(context.Background(), &entity.ActivateZoneEncryptionKey{ZoneID: zoneID, KeyID: keyID}); err != nil {
		t.Fatalf("activate key: %v", err)
	}
	if fanout.Calls != 1 || fanout.Key != "hierarchy_zone_payload_key:"+zoneID.String() || fanout.Payload != nil {
		t.Fatalf("expected key-only fanout invalidation, calls=%d key=%q payload=%v", fanout.Calls, fanout.Key, fanout.Payload)
	}
}

func TestResolveZonePayloadKeyDoesNotNegativeCacheUnavailableOutcome(t *testing.T) {
	zoneID := uuid.MustParse("10000000-0000-7000-8000-000000000030")
	repository := &mocks.ZoneEncryptionKeyRepository{ResolveErr: hierarchyTaxonomy.ErrPreconditionFailed}
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	defer registry.L1.Close()
	service := serviceimpl.NewZoneEncryptionKeyService(repository, registry, &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())

	unavailable, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID})
	if err != nil || unavailable.Available {
		t.Fatalf("expected explicit unavailable result, got result=%+v err=%v", unavailable, err)
	}
	repository.ResolveErr = nil
	repository.ResolveResult = &entity.ResolveZonePayloadKey{
		ZoneID: zoneID, KeyID: uuid.MustParse("10000000-0000-7000-8000-000000000031"),
		PublicKey: bytes.Repeat([]byte{0x61}, 32), ReadyFor: time.Minute,
	}
	available, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID})
	if err != nil || !available.Available {
		t.Fatalf("expected recovery on next request, got result=%+v err=%v", available, err)
	}
	if repository.ResolveCalls != 2 {
		t.Fatalf("unavailable result must not be cached, repository calls=%d", repository.ResolveCalls)
	}
}

func TestResolveZonePayloadKeyHardDeadlineOverridesCacheTTLJitter(t *testing.T) {
	zoneID := uuid.MustParse("10000000-0000-7000-8000-000000000040")
	repository := &mocks.ZoneEncryptionKeyRepository{ResolveResult: &entity.ResolveZonePayloadKey{
		ZoneID: zoneID, KeyID: uuid.MustParse("10000000-0000-7000-8000-000000000041"),
		PublicKey: bytes.Repeat([]byte{0x71}, 32), ReadyFor: 350 * time.Millisecond,
	}}
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	defer registry.L1.Close()
	service := serviceimpl.NewZoneEncryptionKeyService(repository, registry, &mocks.CacheFanout{}, observability.NewNoopWorkflowRecorder())

	if first, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID}); err != nil || !first.Available {
		t.Fatalf("resolve initial key: result=%+v err=%v", first, err)
	}
	time.Sleep(150 * time.Millisecond)
	if second, err := service.ResolveZonePayloadKey(context.Background(), &entity.ResolveZonePayloadKey{ZoneID: zoneID}); err != nil || !second.Available {
		t.Fatalf("reload expired hard lease: result=%+v err=%v", second, err)
	}
	if repository.ResolveCalls != 2 {
		t.Fatalf("hard readiness deadline must force reload, repository calls=%d", repository.ResolveCalls)
	}
}
