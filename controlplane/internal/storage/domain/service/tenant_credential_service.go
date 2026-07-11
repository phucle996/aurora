package storageSvcInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket doanh nghiệp.
type TenantCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket doanh nghiệp.
	CreateCredential(ctx context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Lấy chi tiết thông tin Credentials.
	GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket doanh nghiệp.
	ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Thu hồi / Xóa bỏ Access Key.
	RevokeCredential(ctx context.Context, credID uuid.UUID, userID uuid.UUID) error
}
