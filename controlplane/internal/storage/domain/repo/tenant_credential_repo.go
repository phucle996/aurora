package storageRepoInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantCredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của Bucket doanh nghiệp.
type TenantCredentialRepo interface {
	// [COMMENT]: Lưu trữ Access Key mới liên kết với Bucket doanh nghiệp cùng sự kiện Outbox.
	Create(ctx context.Context, cred *storageEntity.TenantCredential, outbox *storageEntity.StorageOutboxRecord) error
	
	// [COMMENT]: Truy xuất thông tin Access Key theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Liệt kê toàn bộ Access Keys thuộc một Bucket doanh nghiệp.
	ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Xóa thông tin Access Key khỏi Database cùng sự kiện Outbox (scoping theo struct params).
	Delete(ctx context.Context, param *storageEntity.DeleteTenantCredential, outbox *storageEntity.StorageOutboxRecord) error
}
