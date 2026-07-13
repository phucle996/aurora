package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantBucketService quản lý các nghiệp vụ Bucket dành riêng cho đối tượng Doanh nghiệp (Enterprise).
// Suspend/Resume đã bị loại bỏ — bucket không có lifecycle state, chỉ tồn tại hoặc bị xóa.
type TenantBucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới cho Tenant, tự gen và trả về credential 1 lần duy nhất.
	CreateBucketForTenant(ctx context.Context, param *storageEntity.CreateTenantBucket) (*storageEntity.CreatedBucketResult, error)

	// [COMMENT]: Xem chi tiết thông tin Bucket.
	GetBucket(ctx context.Context, bucketID uuid.UUID) (*storageEntity.TenantBucket, error)

	// [COMMENT]: Danh sách Bucket thuộc về một Tenant tại một Zone.
	ListBuckets(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error)

	// [COMMENT]: Thay đổi hạn mức lưu trữ quota của Bucket.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, quotaBytes int64) error

	// [COMMENT]: Yêu cầu xóa Bucket.
	DeleteBucket(ctx context.Context, param *storageEntity.DeleteTenantBucket) error
}
