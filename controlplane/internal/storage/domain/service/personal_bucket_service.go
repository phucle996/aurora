package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: PersonalBucketService quản lý các nghiệp vụ Bucket dành riêng cho cá nhân (Personal Owner).
type PersonalBucketService interface {
	// [COMMENT]: Khởi tạo Bucket mới cho tài khoản cá nhân, tự gen và trả về credential 1 lần duy nhất.
	CreateBucketForPersonal(ctx context.Context, param *storageEntity.CreatePersonalBucket) (*storageEntity.CreatedBucketResult, error)
	
	// [COMMENT]: Xem chi tiết thông tin Bucket cá nhân.
	GetBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Danh sách Bucket thuộc sở hữu cá nhân trong một Workspace tại một Zone.
	ListBuckets(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Thay đổi hạn mức quota cho bucket cá nhân.
	UpdateBucketQuota(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Vô hiệu hóa Bucket cá nhân (Suspend).
	SuspendBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) error
	
	// [COMMENT]: Kích hoạt lại Bucket cá nhân bị suspend.
	ResumeBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) error
	
	// [COMMENT]: Yêu cầu xóa Bucket cá nhân.
	DeleteBucket(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) error
}
