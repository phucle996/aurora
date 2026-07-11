package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: PersonalCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket cá nhân.
type PersonalCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket cá nhân.
	CreateCredential(ctx context.Context, param *storageEntity.CreatePersonalCredential) (*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Lấy chi tiết thông tin Credentials có check quyền sở hữu.
	GetCredential(ctx context.Context, credID uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket cá nhân có check quyền sở hữu.
	ListCredentials(ctx context.Context, bucketID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Thu hồi / Xóa bỏ Access Key.
	RevokeCredential(ctx context.Context, credID uuid.UUID, userID uuid.UUID) error
}
