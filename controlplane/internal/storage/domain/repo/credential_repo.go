package storageRepoInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: TenantCredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của Bucket doanh nghiệp.
type TenantCredentialRepo interface {
	// [COMMENT]: Lưu trữ Access Key mới liên kết với Bucket doanh nghiệp.
	Create(ctx context.Context, cred *storageEntity.TenantCredential) error
	
	// [COMMENT]: Truy xuất thông tin Access Key theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Liệt kê toàn bộ Access Keys thuộc một Bucket doanh nghiệp.
	ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.TenantCredential, error)
	
	// [COMMENT]: Xóa thông tin Access Key khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}

// [COMMENT]: PersonalCredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của Bucket cá nhân.
type PersonalCredentialRepo interface {
	// [COMMENT]: Lưu trữ Access Key mới liên kết với Bucket cá nhân.
	Create(ctx context.Context, cred *storageEntity.PersonalCredential) error
	
	// [COMMENT]: Truy xuất thông tin Access Key theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Liệt kê toàn bộ Access Keys thuộc một Bucket cá nhân.
	ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredential, error)
	
	// [COMMENT]: Xóa thông tin Access Key khỏi Database.
	Delete(ctx context.Context, id uuid.UUID) error
}
