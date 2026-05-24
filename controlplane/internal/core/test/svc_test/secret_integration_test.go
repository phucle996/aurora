package svc_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoImpl "controlplane/internal/core/repository"
	coreSvcImpl "controlplane/internal/core/service"
	"controlplane/internal/core/test/testutil"
	"controlplane/internal/security"
)

func seedFamily(t *testing.T, repo interface {
	EnsureFamily(context.Context, coreEntity.SecretFamily) (*coreEntity.SecretFamily, error)
	CreateSecretVersion(context.Context, coreEntity.SecretVersion) error
	ReplacePrimaryVersion(context.Context, string, string, string, time.Time) error
}, code string, version int, plain string) coreEntity.SecretFamily {
	now := time.Now().UTC()
	family, err := repo.EnsureFamily(context.Background(), coreEntity.SecretFamily{ID: uuid.NewString(), Code: code, Name: code, CreatedAt: now})
	if err != nil {
		t.Fatalf("ensure family: %v", err)
	}
	cipher, err := security.EncryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	ver := coreEntity.SecretVersion{ID: uuid.NewString(), FamilyID: family.ID, Version: version, SecretCiphertext: cipher, SecretFingerprint: fmt.Sprintf("fp-%s-%d", code, version), Status: coreEntity.SecretStatusActive, IsPrimary: true, NotBefore: now, ActivatedAt: &now, RotationReason: "seed", CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateSecretVersion(context.Background(), ver); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := repo.ReplacePrimaryVersion(context.Background(), family.ID, ver.ID, "", now); err != nil {
		t.Fatalf("replace primary: %v", err)
	}
	return *family
}

func TestConcurrentRotatePreservesInvariant(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_rotate"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)
	family := seedFamily(t, repo, "access_token", 1, "secret-one")
	rotator := coreSvcImpl.NewSecretRotationService(repo, nil)

	makeInput := func(id string, version int, secret string) coreEntity.RotateSecretFamilyInput {
		cipher, err := security.EncryptSecret(secret)
		if err != nil {
			t.Fatalf("encrypt rotate secret: %v", err)
		}
		return coreEntity.RotateSecretFamilyInput{FamilyCode: family.Code, TTL: 2 * time.Hour, NewVersion: &coreEntity.SecretVersion{ID: uuid.NewString(), Version: version, SecretCiphertext: cipher, SecretFingerprint: "fp-" + id}}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := rotator.RotateSecretFamily(context.Background(), makeInput("ver-2", 2, "secret-two"))
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := rotator.RotateSecretFamily(context.Background(), makeInput("ver-3", 3, "secret-three"))
		errCh <- err
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent rotate error: %v", err)
		}
	}

	versions, err := repo.ListVersionsByFamilyID(context.Background(), family.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) < 1 || len(versions) > 2 {
		t.Fatalf("versions count = %d, want 1..2", len(versions))
	}
	primaryCount := 0
	for _, item := range versions {
		if item.IsPrimary {
			primaryCount++
		}
	}
	if primaryCount != 1 {
		t.Fatalf("primary count = %d, want 1", primaryCount)
	}
}

func TestPubSubInvalidateReloadsRemoteCache(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_pubsub"))
	cfg.Security.SecretCacheTTL = time.Hour
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)
	family := seedFamily(t, repo, "refresh_token", 1, "secret-old")

	readA := coreSvcImpl.NewSecretReadService(repo)
	providerA := coreCache.NewCacheAsideSecretProviderWithTTL(readA, time.Hour)
	busA := coreCache.NewRedisSecretInvalidationBus(rdb, providerA, "node-a")
	listenCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = busA.Listen(listenCtx) }()

	primaryBefore, err := providerA.GetPrimary(context.Background(), family.Code)
	if err != nil {
		t.Fatalf("providerA warm: %v", err)
	}
	if primaryBefore.Secret != "secret-old" {
		t.Fatalf("primary before = %q, want secret-old", primaryBefore.Secret)
	}

	busB := coreCache.NewRedisSecretInvalidationBus(rdb, providerA, "node-b")
	rotatorB := coreSvcImpl.NewSecretRotationService(repo, busB)
	cipherNew, err := security.EncryptSecret("secret-new")
	if err != nil {
		t.Fatalf("encrypt new secret: %v", err)
	}
	if _, err := rotatorB.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{FamilyCode: family.Code, TTL: 2 * time.Hour, NewVersion: &coreEntity.SecretVersion{ID: uuid.NewString(), Version: 2, SecretCiphertext: cipherNew, SecretFingerprint: "fp-refresh-2"}}); err != nil {
		t.Fatalf("rotate with pubsub: %v", err)
	}

	testutil.WaitUntil(t, 2*time.Second, func() bool {
		primaryAfter, err := providerA.GetPrimary(context.Background(), family.Code)
		return err == nil && primaryAfter.Secret == "secret-new"
	})
}
