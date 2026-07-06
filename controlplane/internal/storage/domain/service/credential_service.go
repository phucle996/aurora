package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket doanh nghiệp.
type TenantCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket doanh nghiệp.
	CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Lấy chi tiết thông tin Credentials.
	GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket doanh nghiệp.
	ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Thu hồi / Xóa bỏ Access Key.
	RevokeCredential(ctx context.Context, credID uuid.UUID) error
}

// [COMMENT]: PersonalCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket cá nhân.
type PersonalCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket cá nhân.
	CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Lấy chi tiết thông tin Credentials.
	GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket cá nhân.
	ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Thu hồi / Xóa bỏ Access Key.
	RevokeCredential(ctx context.Context, credID uuid.UUID) error
}
