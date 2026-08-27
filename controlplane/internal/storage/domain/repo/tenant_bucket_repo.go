package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// TenantBucketRepo định nghĩa các phương thức giao tiếp CSDL cho Bucket Doanh nghiệp (Enterprise).
type TenantBucketRepo interface {
	// Create tạo mới Bucket + Credential + Outbox Record trong một atomic CTE duy nhất.
	Create(ctx context.Context, bucket *storageEntity.TenantBucket, credential *storageEntity.TenantCredential, actorUserID uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error

	// GetByID tìm kiếm thông tin chi tiết của một Bucket theo ID và xác thực quyền qua workspace, tenant, user active và zone.
	GetByID(ctx context.Context, id uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) (*storageEntity.TenantBucket, error)

	// GetByName tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error)

	// ListByWorkspace liệt kê các Bucket thuộc sở hữu của một Workspace trong Tenant và Zone cụ thể.
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)

	// ListNamesByWorkspace liệt kê danh sách tên vật lý của tất cả các bucket trong workspace và zone phục vụ kiểm tra trùng tên.
	ListNamesByWorkspace(ctx context.Context, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]string, error)

	// UpdateQuota cập nhật hạn mức lưu trữ quota của Bucket và ghi nhận outbox.
	UpdateQuota(ctx context.Context, param *storageEntity.UpdateTenantBucketQuota, outbox *storageEntity.StorageOutboxRecord) error

	// UpdateVersioning cập nhật trạng thái Versioning của Bucket và ghi nhận outbox.
	UpdateVersioning(ctx context.Context, param *storageEntity.UpdateTenantBucketVersioning, outbox *storageEntity.StorageOutboxRecord) (*storageEntity.TenantBucket, error)

	// UpdateLifecycle cập nhật cấu hình Lifecycle Rules của Bucket và ghi nhận outbox.
	UpdateLifecycle(ctx context.Context, param *storageEntity.UpdateTenantBucketLifecycle, outbox *storageEntity.StorageOutboxRecord) (*storageEntity.TenantBucket, error)

	// Delete xóa vĩnh viễn bản ghi Bucket ra khỏi Database và ghi nhận outbox.
	Delete(ctx context.Context, id uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error
}
