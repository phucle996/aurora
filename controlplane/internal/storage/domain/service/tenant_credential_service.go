package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket doanh nghiệp.
type TenantCredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket doanh nghiệp, trả về struct kết quả chứa raw Secret Key.
	CreateCredential(ctx context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.CreatedTenantCredential, error)

	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket doanh nghiệp.
	ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error)

	// [COMMENT]: Xóa bỏ Access Key với xác thực scope bucket.
	DeleteCredential(ctx context.Context, param *storageEntity.DeleteTenantCredential) error
}
