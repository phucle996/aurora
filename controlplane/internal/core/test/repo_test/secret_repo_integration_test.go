package repo_test

import (
	"context"
	"testing"
	"time"

	coreRepoImpl "controlplane/internal/core/repository"
	"controlplane/internal/core/test/testutil"
)

func TestAcquireFamilyBootstrapLockBlocksSameFamily(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_lock_same"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)

	lock1, err := repo.AcquireFamilyBootstrapLock(context.Background(), "access_token")
	if err != nil {
		t.Fatalf("lock1: %v", err)
	}
	defer lock1.Release(context.Background())

	acquired := make(chan time.Time, 1)
	go func() {
		lock2, err := repo.AcquireFamilyBootstrapLock(context.Background(), "access_token")
		if err != nil {
			return
		}
		acquired <- time.Now()
		_ = lock2.Release(context.Background())
	}()

	select {
	case <-acquired:
		t.Fatal("second same-family lock acquired before first release")
	case <-time.After(200 * time.Millisecond):
	}

	releaseAt := time.Now()
	_ = lock1.Release(context.Background())

	select {
	case got := <-acquired:
		if got.Before(releaseAt) {
			t.Fatal("second same-family lock acquired before release timestamp")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second same-family lock did not acquire after release")
	}
}

func TestAcquireFamilyBootstrapLockDoesNotBlockDifferentFamily(t *testing.T) {
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_lock_diff"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)
	repo := coreRepoImpl.NewSecretRepository(cfg, db)

	lock1, err := repo.AcquireFamilyBootstrapLock(context.Background(), "access_token")
	if err != nil {
		t.Fatalf("lock1: %v", err)
	}
	defer lock1.Release(context.Background())

	acquired := make(chan struct{}, 1)
	go func() {
		lock2, err := repo.AcquireFamilyBootstrapLock(context.Background(), "refresh_token")
		if err != nil {
			return
		}
		acquired <- struct{}{}
		_ = lock2.Release(context.Background())
	}()

	select {
	case <-acquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("different-family lock should not block")
	}
}
