package storageModel

import (
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalBucket đại diện cho cấu trúc bảng personal_buckets trong PostgreSQL.
// Cột status đã bị drop khỏi schema (migration 000003).
type PersonalBucket struct {
	ID                 uuid.UUID `db:"id"`
	Name               string    `db:"name"`
	WorkspaceID        uuid.UUID `db:"workspace_id"`
	ZoneID             uuid.UUID `db:"zone_id"`
	CapacityQuotaBytes int64     `db:"capacity_quota_bytes"`
	UsedBytes          int64     `db:"used_bytes"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

// [COMMENT]: TenantBucket đại diện cho cấu trúc bảng tenant_buckets trong PostgreSQL.
// Cột status đã bị drop khỏi schema (migration 000003).
type TenantBucket struct {
	ID                 uuid.UUID `db:"id"`
	Name               string    `db:"name"`
	WorkspaceID        uuid.UUID `db:"workspace_id"`
	ZoneID             uuid.UUID `db:"zone_id"`
	TenantID           uuid.UUID `db:"tenant_id"`
	CapacityQuotaBytes int64     `db:"capacity_quota_bytes"`
	UsedBytes          int64     `db:"used_bytes"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

// [COMMENT]: TenantBucketEntityToModel chuyển đổi Domain Entity sang Database Model.
func TenantBucketEntityToModel(e *storageEntity.TenantBucket) *TenantBucket {
	if e == nil {
		return nil
	}
	return &TenantBucket{
		ID:                 e.ID,
		Name:               e.Name,
		WorkspaceID:        e.WorkspaceID,
		ZoneID:             e.ZoneID,
		TenantID:           e.TenantID,
		CapacityQuotaBytes: e.CapacityQuotaBytes,
		UsedBytes:          e.UsedBytes,
		CreatedAt:          e.CreatedAt,
		UpdatedAt:          e.UpdatedAt,
	}
}

// [COMMENT]: TenantBucketModelToEntity chuyển đổi Database Model sang Domain Entity.
func TenantBucketModelToEntity(m *TenantBucket) *storageEntity.TenantBucket {
	if m == nil {
		return nil
	}
	return &storageEntity.TenantBucket{
		ID:                 m.ID,
		Name:               m.Name,
		WorkspaceID:        m.WorkspaceID,
		ZoneID:             m.ZoneID,
		TenantID:           m.TenantID,
		CapacityQuotaBytes: m.CapacityQuotaBytes,
		UsedBytes:          m.UsedBytes,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}
