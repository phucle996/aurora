package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantBucketService quản lý các nghiệp vụ Bucket dành riêng cho đối tượng Doanh nghiệp (Enterprise).
type TenantBucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới cho Tenant (Thực thi DB transaction + trigger outbox tạo trên MinIO).
	CreateBucketForTenant(ctx context.Context, tenantID uuid.UUID, workspaceID uuid.UUID, zoneID uuid.UUID, name string, quotaBytes int64) error
	
	// [COMMENT]: Xem chi tiết thông tin Bucket.
	GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.TenantBucket, error)
	
	// [COMMENT]: Danh sách Bucket thuộc về một Tenant tại một Zone.
	ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)
	
	// [COMMENT]: Thay đổi hạn mức lưu trữ quota của Bucket.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Vô hiệu hóa Bucket (Suspend).
	SuspendBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Kích hoạt lại Bucket bị suspend.
	ResumeBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Yêu cầu xóa Bucket (đánh dấu deleted và trigger outbox xóa vật lý).
	DeleteBucket(ctx context.Context, bucketID uuid.UUID) error
}

// [COMMENT]: PersonalBucketService quản lý các nghiệp vụ Bucket dành riêng cho cá nhân (Personal Owner).
type PersonalBucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới cho tài khoản cá nhân.
	CreateBucketForPersonal(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID, zoneID uuid.UUID, name string, quotaBytes int64) error
	
	// [COMMENT]: Xem chi tiết thông tin Bucket cá nhân.
	GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Danh sách Bucket thuộc sở hữu cá nhân trong một Workspace.
	ListBuckets(ctx context.Context, workspaceID uuid.UUID) ([]*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Thay đổi hạn mức quota cho bucket cá nhân.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Vô hiệu hóa Bucket cá nhân (Suspend).
	SuspendBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Kích hoạt lại Bucket cá nhân bị suspend.
	ResumeBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Yêu cầu xóa Bucket cá nhân.
	DeleteBucket(ctx context.Context, bucketID uuid.UUID) error
}
