package storageRepo

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: CredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của MinIO.
type CredentialRepo interface {
	// [COMMENT]: Lưu trữ Access Key mới liên kết với Bucket.
	Create(ctx context.Context, cred *storageEntity.Credential) error
	
	// [COMMENT]: Truy xuất thông tin Access Key theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.Credential, error)
	
	// [COMMENT]: Liệt kê toàn bộ Access Keys thuộc một Bucket.
	ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.Credential, error)
	
	// [COMMENT]: Xóa thông tin Access Key khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}
