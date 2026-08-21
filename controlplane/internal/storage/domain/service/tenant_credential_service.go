package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// TenantCredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys cho Bucket doanh nghiệp.
type TenantCredentialService interface {
	// CreateCredential khởi tạo một cặp Access Key mới cho Bucket doanh nghiệp, trả về struct kết quả chứa raw Secret Key.
	CreateCredential(ctx context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.CreatedTenantCredential, error)

	// ListCredentials liệt kê danh sách các Keys đang hoạt động của Bucket doanh nghiệp.
	ListCredentials(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantCredential, error)

	// DeleteCredential xóa bỏ Access Key với xác thực scope bucket.
	DeleteCredential(ctx context.Context, param *storageEntity.DeleteTenantCredential) error

	// ListAccessKeys lấy danh sách access keys của toàn bộ credentials liên kết với bucket này.
	ListAccessKeys(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]string, error)
}
