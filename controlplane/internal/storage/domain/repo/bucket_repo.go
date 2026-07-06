package storageRepoInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantBucketRepo định nghĩa các phương thức giao tiếp CSDL cho Bucket Doanh nghiệp (Enterprise).
type TenantBucketRepo interface {
	// [COMMENT]: Tạo mới bản ghi Bucket trong Database.
	Create(ctx context.Context, bucket *storageEntity.TenantBucket, outbox *storageEntity.StorageOutboxRecord) error
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantBucket, error)
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error)
	
	// [COMMENT]: Liệt kê các Bucket thuộc sở hữu của một Tenant trong một Zone cụ thể.
	ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)
	
	// [COMMENT]: Cập nhật trạng thái vận hành của Bucket (Active, Suspended, Deleted).
	UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error
	
	// [COMMENT]: Cập nhật hạn mức lưu trữ quota của Bucket.
	UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Xóa vĩnh viễn bản ghi Bucket ra khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}

// [COMMENT]: PersonalBucketRepo định nghĩa các phương thức giao tiếp CSDL cho Bucket Cá nhân (Individual).
type PersonalBucketRepo interface {
	// [COMMENT]: Tạo mới bản ghi Bucket trong Database.
	Create(ctx context.Context, bucket *storageEntity.PersonalBucket, outbox *storageEntity.StorageOutboxRecord) error
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.PersonalBucket, error)

	// [COMMENT]: Liệt kê các Bucket thuộc sở hữu của một Workspace cụ thể.
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Cập nhật trạng thái vận hành của Bucket (Active, Suspended, Deleted).
	UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error
	
	// [COMMENT]: Cập nhật hạn mức lưu trữ quota của Bucket.
	UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Xóa vĩnh viễn bản ghi Bucket ra khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}
