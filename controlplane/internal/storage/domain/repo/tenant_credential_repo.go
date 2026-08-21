package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

// TenantCredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của Bucket doanh nghiệp.
type TenantCredentialRepo interface {
	// Create lưu trữ Access Key mới liên kết với Bucket doanh nghiệp cùng sự kiện Outbox.
	Create(ctx context.Context, cred *storageEntity.TenantCredential, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID, outbox *storageEntity.StorageOutboxRecord) error

	// GetByID lấy chi tiết credential kèm xác thực quyền sở hữu bucket.
	GetByID(ctx context.Context, id uuid.UUID, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) (*storageEntity.TenantCredential, error)

	// ListByBucket liệt kê toàn bộ Access Keys thuộc một Bucket doanh nghiệp.
	ListByBucket(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantCredential, error)

	// Delete xóa thông tin Access Key khỏi Database cùng sự kiện Outbox.
	Delete(ctx context.Context, param *storageEntity.DeleteTenantCredential, outbox *storageEntity.StorageOutboxRecord) error

	// ListAccessKeys lấy danh sách access keys của toàn bộ credentials liên kết với bucket này.
	ListAccessKeys(ctx context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]string, error)
}
