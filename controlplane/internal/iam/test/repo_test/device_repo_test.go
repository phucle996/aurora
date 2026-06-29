package repo_test

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/iam/test/testutil"

	"github.com/google/uuid"
)

func seedUserAndDevice(t *testing.T, schema string, repoCfg func() (string, string), exec func(query string, args ...any) error) (uuid.UUID, uuid.UUID) {
	t.Helper()
	username, email := repoCfg()
	userID := uuid.New()
	now := time.Now().UTC()
	if err := exec(
		"INSERT INTO "+schema+".users (id, username, email, phone, password_hash, status, created_at, updated_at) VALUES ($1,$2,$3,NULL,$4,'active',$5,$6)",
		userID, username, email, "hash", now, now,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	deviceID := uuid.New()
	if err := exec(
		"INSERT INTO "+schema+".devices (id,user_id,device_name,device_type,os_name,browser_name,public_key,public_key_fingerprint,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'new',$9,$10)",
		deviceID, userID, "Chrome", "browser", "Linux", "Chrome", "pk", "fp-"+deviceID.String(), now, now,
	); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return userID, deviceID
}

func TestDeviceRepositoryListAndRevoke(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_device_repo"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	repo := iamRepoImpl.NewDeviceRepository(cfg, db)
	ctx := context.Background()

	userID, deviceID := seedUserAndDevice(t, cfg.SchemaSQL.IAM, func() (string, string) {
		return testutil.UniqueIdentity("device_repo")
	}, func(query string, args ...any) error {
		_, err := db.Exec(ctx, query, args...)
		return err
	})

	items, err := repo.ListDevicesByUserID(ctx, userID, 20, 0)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(items) != 1 || items[0].ID != deviceID.String() {
		t.Fatalf("unexpected devices: %+v", items)
	}

	err = repo.RevokeDeviceByIDAndUserID(ctx, deviceID, userID, uuid.Nil)
	if err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	// [COMMENT]: Liệt kê các thiết bị để kiểm tra trạng thái của thiết bị đã revoke.
	devices, err := repo.ListDevicesByUserID(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	got := devices[0]
	if got.Status != iamEntity.DeviceStatusRevoked {
		t.Fatalf("expected revoked status, got %s", got.Status)
	}
	if got.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}
}

func TestDeviceRepositoryRevokeOtherDevices(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_device_repo_bulk"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	repo := iamRepoImpl.NewDeviceRepository(cfg, db)
	ctx := context.Background()

	userID, keepID := seedUserAndDevice(t, cfg.SchemaSQL.IAM, func() (string, string) {
		return testutil.UniqueIdentity("device_repo_keep")
	}, func(query string, args ...any) error {
		_, err := db.Exec(ctx, query, args...)
		return err
	})

	now := time.Now().UTC()
	otherID := uuid.New()
	_, err := db.Exec(ctx,
		"INSERT INTO "+cfg.SchemaSQL.IAM+".devices (id,user_id,device_name,public_key,public_key_fingerprint,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'recognized',$6,$7)",
		otherID, userID, "Firefox", "pk2", "fp-"+otherID.String(), now, now,
	)
	if err != nil {
		t.Fatalf("seed second device: %v", err)
	}

	affected, err := repo.RevokeOtherDevicesByUserID(ctx, userID, &keepID)
	if err != nil {
		t.Fatalf("revoke others: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected affected=1, got %d", affected)
	}
}
