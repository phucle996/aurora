package integration_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	"controlplane/internal/iam/test/testutil"
)

// TestDeviceUpsertConcurrentSameClientDeviceID kiểm tra: hai login đồng thời
// với cùng (user_id, client_device_id) chỉ tạo 1 record.
func TestDeviceUpsertConcurrentSameClientDeviceID(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_cdid_race"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)

	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, nil, nil, &testutil.SessionServiceClientMock{})
	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	authSvc := iamSvcImpl.NewAuthService(cfg, authRepo, nil, nil, deviceSvc, nil, nil, nil, &testutil.SessionServiceClientMock{})
	_ = authSvc // keep references resolved

	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if _, err := db.Exec(context.Background(),
		"INSERT INTO "+cfg.SchemaSQL.IAM+".users (id, username, email, password_hash, status) VALUES ($1, $2, $3, 'pwd', 'active') ON CONFLICT (id) DO NOTHING",
		userID, "race-user", "race@example.com",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cdid := "cdid-race-1"
	pubKey := "race-public-key"

	var wg sync.WaitGroup
	var firstID, secondID atomic.Value
	const concurrency = 8
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cd := cdid
			device := iamEntity.Device{
				UserID:               userID,
				DeviceName:           "race-laptop",
				PublicKey:            pubKey,
				PublicKeyAlg:         "Ed25519",
				PublicKeyFingerprint: "fp-race",
				ClientDeviceID:       &cd,
				UpdatedAt:            time.Now().UTC(),
			}
			out, err := deviceRepo.UpsertLoginDevice(context.Background(), device)
			if err != nil || out == nil {
				return
			}
			if i == 0 {
				firstID.Store(out.ID)
			} else {
				secondID.Store(out.ID)
			}
		}(i)
	}
	wg.Wait()

	rows, err := db.Query(context.Background(),
		"SELECT id FROM "+cfg.SchemaSQL.IAM+".devices WHERE user_id=$1 AND client_device_id=$2",
		userID, cdid,
	)
	if err != nil {
		t.Fatalf("query devices: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 device row for (user, cdid), got %d", count)
	}
}
