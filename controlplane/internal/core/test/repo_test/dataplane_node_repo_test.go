package repo_test

import (
	"context"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoImpl "controlplane/internal/core/repository"
	"controlplane/internal/core/test/testutil"

	"github.com/google/uuid"
)

// TestDataplaneNodeRepo_Integration kiểm thử tích hợp đầy đủ PostgreSQL cho Dataplane Node Repository.
func TestDataplaneNodeRepo_Integration(t *testing.T) {
	// Step 1: Khởi tạo cấu hình và schema Postgres cô lập phục vụ test.
	cfg := testutil.NewCoreTestConfig(testutil.UniqueSchema("core_test_dataplane_repo"))
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareCoreSchema(t, cfg, db)

	repo := coreRepoImpl.NewDataplaneNodeRepoImpl(cfg, db)
	ctx := context.Background()

	zoneID, _ := uuid.NewV7()
	clusterID, _ := uuid.NewV7()

	// 1. Chèn Zone cha vào bảng zones để thỏa mãn ràng buộc khóa ngoại (Foreign Key Constraint)
	_, err := db.Exec(ctx, "INSERT INTO zones (id, code, name, status, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)", 
		zoneID, "zone-a", "Zone A", "active", time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to insert parent zone: %v", err)
	}

	// Khởi tạo thực thể DataplaneNode
	node := coreEntity.DataplaneNode{
		ID:        clusterID.String(),
		ZoneID:    zoneID.String(),
		Endpoint:  "dp-gateway.zone-a.internal:9000",
		Status:    coreEntity.DataplaneNodeStatusReady,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// 2. Kiểm thử RegisterCluster (Insert mới)
	t.Run("RegisterNewCluster", func(t *testing.T) {
		err := repo.RegisterCluster(ctx, node)
		if err != nil {
			t.Fatalf("RegisterCluster failed: %v", err)
		}
	})

	// 3. Kiểm thử RegisterCluster ON CONFLICT (Idempotent UPSERT)
	t.Run("RegisterClusterUPSERT", func(t *testing.T) {
		// Thay đổi Endpoint để kiểm tra UPSERT có ghi đè không
		node.Endpoint = "dp-gateway-updated.zone-a.internal:9000"
		err := repo.RegisterCluster(ctx, node)
		if err != nil {
			t.Fatalf("UPSERT RegisterCluster failed: %v", err)
		}

		// Đọc lại để kiểm tra endpoint đã được cập nhật
		fetched, err := repo.GetCluster(ctx, clusterID)
		if err != nil {
			t.Fatalf("GetCluster failed: %v", err)
		}
		if fetched.Endpoint != "dp-gateway-updated.zone-a.internal:9000" {
			t.Errorf("Expected endpoint updated, got %s", fetched.Endpoint)
		}
	})

	// 4. Kiểm thử GetClusterByZone
	t.Run("GetClusterByZone", func(t *testing.T) {
		fetched, err := repo.GetClusterByZone(ctx, zoneID)
		if err != nil {
			t.Fatalf("GetClusterByZone failed: %v", err)
		}
		if fetched.ID != clusterID.String() {
			t.Errorf("Expected cluster ID %s, got %s", clusterID.String(), fetched.ID)
		}
	})

	// 5. Kiểm thử UpdateClusterStatus
	t.Run("UpdateClusterStatus", func(t *testing.T) {
		err := repo.UpdateClusterStatus(ctx, clusterID, coreEntity.DataplaneNodeStatusStale)
		if err != nil {
			t.Fatalf("UpdateClusterStatus failed: %v", err)
		}

		fetched, err := repo.GetCluster(ctx, clusterID)
		if err != nil {
			t.Fatalf("GetCluster failed: %v", err)
		}
		if fetched.Status != coreEntity.DataplaneNodeStatusStale {
			t.Errorf("Expected status 'stale', got '%s'", fetched.Status)
		}
	})

	// 6. Kiểm thử ListReadyClusters
	t.Run("ListReadyClusters", func(t *testing.T) {
		// Hiện tại cluster đang ở trạng thái 'stale', danh sách ready phải rỗng
		readyList, err := repo.ListReadyClusters(ctx)
		if err != nil {
			t.Fatalf("ListReadyClusters failed: %v", err)
		}
		for _, c := range readyList {
			if c.ID == clusterID.String() {
				t.Error("Stale cluster returned in ListReadyClusters")
			}
		}

		// Cập nhật lại sang 'ready'
		err = repo.UpdateClusterStatus(ctx, clusterID, coreEntity.DataplaneNodeStatusReady)
		if err != nil {
			t.Fatalf("Restore status to ready failed: %v", err)
		}

		readyList, err = repo.ListReadyClusters(ctx)
		if err != nil {
			t.Fatalf("ListReadyClusters failed: %v", err)
		}
		found := false
		for _, c := range readyList {
			if c.ID == clusterID.String() {
				found = true
				break
			}
		}
		if !found {
			t.Error("Ready cluster not returned in ListReadyClusters")
		}
	})
}
