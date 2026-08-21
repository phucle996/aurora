package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: PersonalBucketService quản lý các nghiệp vụ Bucket dành riêng cho cá nhân (Personal Owner).
// Suspend/Resume đã bị loại bỏ — bucket không có lifecycle state, chỉ tồn tại hoặc bị xóa.
type PersonalBucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới cho tài khoản cá nhân, tự gen và trả về credential 1 lần duy nhất.
	CreateBucketForPersonal(ctx context.Context, param *storageEntity.CreatePersonalBucket) (*storageEntity.CreatedBucketResult, error)

	// [COMMENT]: Xem chi tiết thông tin Bucket cá nhân.
	GetBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error)

	// [COMMENT]: Danh sách Bucket thuộc sở hữu cá nhân trong một Workspace tại một Zone.
	ListBuckets(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error)

	// [COMMENT]: Danh sách tên vật lý của các Bucket thuộc sở hữu cá nhân trong một Workspace tại một Zone.
	ListBucketNames(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]string, error)

	// [COMMENT]: Thay đổi hạn mức quota cho bucket cá nhân.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, quotaBytes int64) error

	// [COMMENT]: Bật hoặc tắt versioning cho bucket cá nhân.
	UpdateBucketVersioning(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, versioningEnabled bool) (*storageEntity.PersonalBucket, error)

	// [COMMENT]: Lấy cấu hình lifecycle rules của bucket cá nhân.
	GetBucketLifecycle(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]storageEntity.BucketLifecycleRule, error)

	// [COMMENT]: Cập nhật cấu hình lifecycle rules cho bucket cá nhân.
	UpdateBucketLifecycle(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, rules []storageEntity.BucketLifecycleRule) (*storageEntity.PersonalBucket, error)

	// [COMMENT]: Yêu cầu xóa Bucket cá nhân.
	DeleteBucket(ctx context.Context, param *storageEntity.DeletePersonalBucket) error
}
