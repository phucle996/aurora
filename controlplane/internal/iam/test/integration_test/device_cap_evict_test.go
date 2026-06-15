package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/iam/test/testutil"
)


func TestEvictExcessDevicesAtomic(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_cap_evict"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)

	repo := iamRepoImpl.NewDeviceRepository(cfg, db)

	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if _, err := db.Exec(context.Background(),
		"INSERT INTO "+cfg.SchemaSQL.IAM+".users (id, username, email, password_hash, status) VALUES ($1, $2, $3, 'pwd', 'active') ON CONFLICT (id) DO NOTHING",
		userID, "cap-user", "cap@example.com",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// seed 60 active devices with strictly increasing last_seen_at ordering
	base := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 60; i++ {
		cd := fmt.Sprintf("cdid-cap-%02d", i)
		dev := iamEntity.Device{
			UserID:               userID,
			DeviceName:           fmt.Sprintf("dev-%02d", i),
			PublicKey:            fmt.Sprintf("pk-cap-%02d", i),
			PublicKeyAlg:         "Ed25519",
			PublicKeyFingerprint: fmt.Sprintf("fp-cap-%02d", i),
			ClientDeviceID:       &cd,
			UpdatedAt:            base.Add(time.Duration(i) * time.Second),
		}
		if _, err := repo.UpsertLoginDevice(context.Background(), dev); err != nil {
			t.Fatalf("seed dev %d: %v", i, err)
		}
	}

	// override last_seen_at according to seed order so ranking is deterministic
	for i := 0; i < 60; i++ {
		cd := fmt.Sprintf("cdid-cap-%02d", i)
		seen := base.Add(time.Duration(i) * time.Second)
		if _, err := db.Exec(context.Background(),
			"UPDATE "+cfg.SchemaSQL.IAM+".devices SET last_seen_at=$1, status='recognized' WHERE user_id=$2 AND client_device_id=$3",
			seen, userID, cd,
		); err != nil {
			t.Fatalf("update seen: %v", err)
		}
	}

	// run evict cap=50
	evicted, err := repo.EvictExcessDevices(context.Background(), userID, 50)
	if err != nil {
		t.Fatalf("evict err: %v", err)
	}
	// expect 10 evicted (the oldest 10)
	if len(evicted) != 10 {
		t.Fatalf("expected 10 evicted, got %d", len(evicted))
	}

	var active int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM "+cfg.SchemaSQL.IAM+".devices WHERE user_id=$1 AND status != 'revoked'",
		userID,
	).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 50 {
		t.Fatalf("expected 50 active, got %d", active)
	}

	// idempotent rerun
	again, err := repo.EvictExcessDevices(context.Background(), userID, 50)
	if err != nil || len(again) != 0 {
		t.Fatalf("second evict should be noop, got %d err=%v", len(again), err)
	}
}
