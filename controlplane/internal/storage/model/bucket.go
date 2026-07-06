package storageModel

import (
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: PersonalBucket đại diện cho cấu trúc bảng personal_buckets trong PostgreSQL.
type PersonalBucket struct {
	ID                 uuid.UUID `db:"id"`
	Name               string    `db:"name"`
	WorkspaceID        uuid.UUID `db:"workspace_id"`
	ZoneID             uuid.UUID `db:"zone_id"`
	Status             string    `db:"status"`
	CapacityQuotaBytes int64     `db:"capacity_quota_bytes"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

// [COMMENT]: TenantBucket đại diện cho cấu trúc bảng tenant_buckets trong PostgreSQL.
type TenantBucket struct {
	ID                 uuid.UUID `db:"id"`
	Name               string    `db:"name"`
	WorkspaceID        uuid.UUID `db:"workspace_id"`
	ZoneID             uuid.UUID `db:"zone_id"`
	TenantID           uuid.UUID `db:"tenant_id"`
	Status             string    `db:"status"`
	CapacityQuotaBytes int64     `db:"capacity_quota_bytes"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

// [COMMENT]: PersonalBucketEntityToModel chuyển đổi Domain Entity sang Database Model.
func PersonalBucketEntityToModel(e *storageEntity.PersonalBucket) *PersonalBucket {
	if e == nil {
		return nil
	}
	return &PersonalBucket{
		ID:                 e.ID,
		Name:               e.Name,
		WorkspaceID:        e.WorkspaceID,
		ZoneID:             e.ZoneID,
		Status:             string(e.Status),
		CapacityQuotaBytes: e.CapacityQuotaBytes,
		CreatedAt:          e.CreatedAt,
		UpdatedAt:          e.UpdatedAt,
	}
}

// [COMMENT]: PersonalBucketModelToEntity chuyển đổi Database Model sang Domain Entity.
func PersonalBucketModelToEntity(m *PersonalBucket) *storageEntity.PersonalBucket {
	if m == nil {
		return nil
	}
	return &storageEntity.PersonalBucket{
		ID:                 m.ID,
		Name:               m.Name,
		WorkspaceID:        m.WorkspaceID,
		ZoneID:             m.ZoneID,
		Status:             storageEntity.BucketStatus(m.Status),
		CapacityQuotaBytes: m.CapacityQuotaBytes,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
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
		Status:             string(e.Status),
		CapacityQuotaBytes: e.CapacityQuotaBytes,
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
		Status:             storageEntity.BucketStatus(m.Status),
		CapacityQuotaBytes: m.CapacityQuotaBytes,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}
