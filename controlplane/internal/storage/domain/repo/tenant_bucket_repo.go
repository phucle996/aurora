package storageRepoInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantBucketRepo định nghĩa các phương thức giao tiếp CSDL cho Bucket Doanh nghiệp (Enterprise).
type TenantBucketRepo interface {
	// [COMMENT]: Tạo mới Bucket + Credential + Outbox Record trong một atomic CTE duy nhất.
	Create(ctx context.Context, bucket *storageEntity.TenantBucket, credential *storageEntity.TenantCredential, outbox *storageEntity.StorageOutboxRecord) error

	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantBucket, error)

	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.TenantBucket, error)

	// [COMMENT]: Liệt kê các Bucket thuộc sở hữu của một Tenant trong một Zone cụ thể.
	ListByTenantAndZone(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)

	// [COMMENT]: Cập nhật hạn mức lưu trữ quota của Bucket và ghi nhận outbox.
	UpdateQuota(ctx context.Context, id uuid.UUID, quotaBytes int64, outbox *storageEntity.StorageOutboxRecord) error

	// [COMMENT]: Xóa vĩnh viễn bản ghi Bucket ra khỏi Database và ghi nhận outbox.
	Delete(ctx context.Context, id uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error

	// [COMMENT]: Lấy danh sách access keys của toàn bộ credentials liên kết với bucket này.
	ListAccessKeys(ctx context.Context, bucketID uuid.UUID) ([]string, error)
}
