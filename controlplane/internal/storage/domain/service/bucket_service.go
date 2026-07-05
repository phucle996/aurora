package storageSvc

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: BucketService định nghĩa các nghiệp vụ cốt lõi quản lý Object Storage Buckets.
type BucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới (Thực thi DB transaction + trigger outbox tạo trên MinIO).
	CreateBucket(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, name string, quotaBytes int64) (*storageEntity.Bucket, error)
	
	// [COMMENT]: Xem chi tiết thông tin Bucket.
	GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.Bucket, error)
	
	// [COMMENT]: Danh sách Bucket thuộc về một Tenant tại một Zone.
	ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.Bucket, error)
	
	// [COMMENT]: Thay đổi hạn mức lưu trữ quota của Bucket.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Vô hiệu hóa Bucket (Suspend).
	SuspendBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Kích hoạt lại Bucket bị suspend.
	ResumeBucket(ctx context.Context, bucketID uuid.UUID) error
	
	// [COMMENT]: Yêu cầu xóa Bucket (đánh dấu deleted và trigger outbox xóa vật lý).
	DeleteBucket(ctx context.Context, bucketID uuid.UUID) error
}
