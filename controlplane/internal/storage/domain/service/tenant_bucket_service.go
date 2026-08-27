package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// TenantBucketService quản lý các nghiệp vụ Bucket dành riêng cho đối tượng Doanh nghiệp (Enterprise).
type TenantBucketService interface {
	// CreateBucketForTenant khởi tạo Bucket mới cho Tenant, tự gen và trả về credential 1 lần duy nhất.
	CreateBucketForTenant(ctx context.Context, param *storageEntity.CreateTenantBucket) (*storageEntity.CreatedBucketResult, error)

	// GetBucket xem chi tiết thông tin Bucket.
	GetBucket(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) (*storageEntity.TenantBucket, error)

	// ListBuckets lấy danh sách Bucket thuộc về một Workspace trong Tenant và Zone.
	ListBuckets(ctx context.Context, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)

	// UpdateBucketQuota thay đổi hạn mức lưu trữ quota của Bucket.
	UpdateBucketQuota(ctx context.Context, param *storageEntity.UpdateTenantBucketQuota) error

	// UpdateBucketVersioning cập nhật trạng thái Versioning của Bucket.
	UpdateBucketVersioning(ctx context.Context, param *storageEntity.UpdateTenantBucketVersioning) (*storageEntity.TenantBucket, error)

	// GetBucketLifecycle truy vấn cấu hình Lifecycle Rules của Bucket.
	GetBucketLifecycle(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]storageEntity.BucketLifecycleRule, error)

	// UpdateBucketLifecycle cập nhật cấu hình Lifecycle Rules của Bucket.
	UpdateBucketLifecycle(ctx context.Context, param *storageEntity.UpdateTenantBucketLifecycle) (*storageEntity.TenantBucket, error)

	// DeleteBucket xóa Bucket.
	DeleteBucket(ctx context.Context, param *storageEntity.DeleteTenantBucket) error
}
