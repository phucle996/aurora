package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: PersonalCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket cá nhân.
type PersonalCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket cá nhân, trả về struct kết quả chứa raw Secret Key.
	CreateCredential(ctx context.Context, param *storageEntity.CreatePersonalCredential) (*storageEntity.CreatedPersonalCredential, error)

	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket cá nhân có check quyền sở hữu, trả về list item được rút gọn (không gồm BucketID)
	ListCredentials(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error)

	// [COMMENT]: Xóa bỏ Access Key với xác thực scope bucket.
	DeleteCredential(ctx context.Context, param *storageEntity.DeletePersonalCredential) error
}
