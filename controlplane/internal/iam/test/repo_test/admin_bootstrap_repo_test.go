package repo_test

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/iam/test/testutil"
)

func TestAdminBootstrapRepoBootstrapAndRollback(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_bootstrap_repo"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)

	repo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	ctx := context.Background()

	payload := iamEntity.AdminBootstrapPayload{
		Actor:              "tester",
		KeyHash:            "k1",
		ExpiresAt:          time.Now().UTC().Add(24 * time.Hour),
		RecoveryCodeHashes: []string{"a", "b", "c", "d", "e", "f", "g", "h"},
		GeneratedAt:        time.Now().UTC(),
		SecretCiphertext:   "cipher",
	}
	if _, err := repo.Bootstrap(ctx, payload); err != nil {
		t.Fatalf("bootstrap persist: %v", err)
	}

	active, err := repo.GetActiveAdminAPIKey(ctx)
	if err != nil || active == nil {
		t.Fatalf("expected active key, err=%v", err)
	}

	if err := repo.RollbackBootstrap(ctx, payload); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	active, err = repo.GetActiveAdminAPIKey(ctx)
	if err != nil {
		t.Fatalf("get active after rollback: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active key after rollback")
	}
}

func TestAdminBootstrapRepoAdvisoryLockSingleOwner(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_bootstrap_lock"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)

	repo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	ctx := context.Background()

	lock1, err := repo.AcquireBootstrapLock(ctx)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer lock1.Release(ctx)

	if _, err := repo.AcquireBootstrapLock(ctx); err == nil {
		t.Fatalf("expected second lock acquire to fail")
	}
}
