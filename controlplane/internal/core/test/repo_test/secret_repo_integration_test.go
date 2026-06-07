package repo_test

import (
	"context"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoImpl "controlplane/internal/core/repository"
	"controlplane/internal/core/test/testutil"
	"controlplane/internal/security"
)

func TestAcquireSecretTypeBootstrapLockBlocksSameType(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_lock_same"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)

	lock1, err := repo.AcquireSecretTypeBootstrapLock(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("lock1: %v", err)
	}
	defer lock1.Release(context.Background())

	acquired := make(chan time.Time, 1)
	go func() {
		lock2, err := repo.AcquireSecretTypeBootstrapLock(context.Background(), "access_secret")
		if err != nil {
			return
		}
		acquired <- time.Now()
		_ = lock2.Release(context.Background())
	}()

	select {
	case <-acquired:
		t.Fatal("second same-type lock acquired before first release")
	case <-time.After(200 * time.Millisecond):
	}

	releaseAt := time.Now()
	_ = lock1.Release(context.Background())

	select {
	case got := <-acquired:
		if got.Before(releaseAt) {
			t.Fatal("second same-type lock acquired before release timestamp")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second same-type lock did not acquire after release")
	}
}

func TestAcquireSecretTypeBootstrapLockDoesNotBlockDifferentType(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_lock_diff"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)

	lock1, err := repo.AcquireSecretTypeBootstrapLock(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("lock1: %v", err)
	}
	defer lock1.Release(context.Background())

	acquired := make(chan struct{}, 1)
	go func() {
		lock2, err := repo.AcquireSecretTypeBootstrapLock(context.Background(), "refresh_secret")
		if err != nil {
			return
		}
		acquired <- struct{}{}
		_ = lock2.Release(context.Background())
	}()

	select {
	case <-acquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("different-type lock should not block")
	}
}

func TestSaveAndGetSecrets(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)

	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_secrets"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)

	ctx := context.Background()

	// Ensure empty first
	s, err := repo.GetSecretsByType(ctx, "access_secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil secrets initially, got: %#v", s)
	}

	activeCipher, err := security.EncryptSecret("my-active-secret")
	if err != nil {
		t.Fatalf("failed to encrypt active secret: %v", err)
	}
	standbyCipher, err := security.EncryptSecret("my-standby-secret")
	if err != nil {
		t.Fatalf("failed to encrypt standby secret: %v", err)
	}

	now := time.Now().Truncate(time.Microsecond).UTC()
	row := coreEntity.CoreSecretRow{
		SecretType:         "access_secret",
		ActiveSecret:       activeCipher,
		ActiveFingerprint:  "active-fp",
		ActiveCreatedAt:    now,
		StandbySecret:      standbyCipher,
		StandbyFingerprint: "standby-fp",
		StandbyCreatedAt:   now,
		UpdatedAt:          now,
	}

	err = repo.SaveSecrets(ctx, row)
	if err != nil {
		t.Fatalf("failed to save secrets: %v", err)
	}

	// Read back
	s, err = repo.GetSecretsByType(ctx, "access_secret")
	if err != nil {
		t.Fatalf("failed to get secrets: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil secrets")
	}

	if s.SecretType != "access_secret" {
		t.Errorf("expected access_secret, got %s", s.SecretType)
	}
	if string(s.Active.Secret) != "my-active-secret" {
		t.Errorf("expected my-active-secret, got %s", s.Active.Secret)
	}
	if string(s.Standby.Secret) != "my-standby-secret" {
		t.Errorf("expected my-standby-secret, got %s", s.Standby.Secret)
	}

	// Update
	newActiveCipher, err := security.EncryptSecret("new-active")
	if err != nil {
		t.Fatalf("failed to encrypt new active: %v", err)
	}
	newStandbyCipher, err := security.EncryptSecret("my-active-secret")
	if err != nil {
		t.Fatalf("failed to encrypt new standby: %v", err)
	}

	err = repo.UpdateSecrets(ctx, "access_secret", newActiveCipher, "new-active-fp", newStandbyCipher, "active-fp")
	if err != nil {
		t.Fatalf("failed to update secrets: %v", err)
	}

	// Read back updated
	s, err = repo.GetSecretsByType(ctx, "access_secret")
	if err != nil {
		t.Fatalf("failed to get secrets: %v", err)
	}
	if string(s.Active.Secret) != "new-active" || string(s.Standby.Secret) != "my-active-secret" {
		t.Errorf("unexpected updated values: active=%s, standby=%s", s.Active.Secret, s.Standby.Secret)
	}
}
