package storageRepo

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: BucketRepo định nghĩa các phương thức giao tiếp CSDL của Bucket.
type BucketRepo interface {
	// [COMMENT]: Tạo mới bản ghi Bucket trong Database.
	Create(ctx context.Context, bucket *storageEntity.Bucket) error
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.Bucket, error)
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.Bucket, error)
	
	// [COMMENT]: Liệt kê các Bucket thuộc sở hữu của một Tenant trong một Zone cụ thể.
	ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.Bucket, error)
	
	// [COMMENT]: Cập nhật trạng thái vận hành của Bucket (Active, Suspended, Deleted).
	UpdateStatus(ctx context.Context, id uuid.UUID, status storageEntity.BucketStatus) error
	
	// [COMMENT]: Cập nhật hạn mức lưu trữ quota của Bucket.
	UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Xóa vĩnh viễn bản ghi Bucket ra khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}
