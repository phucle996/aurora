package storageSvc

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: CredentialService định nghĩa logic nghiệp vụ quản lý khóa truy cập Access Keys của MinIO.
type CredentialService interface {
	// [COMMENT]: Khởi tạo một cặp Access Key mới cho Bucket (tạo trên DB + sinh key trên MinIO).
	CreateCredential(ctx context.Context, bucketID uuid.UUID, policy string) (*storageEntity.Credential, error)
	
	// [COMMENT]: Lấy chi tiết thông tin Credentials.
	GetCredential(ctx context.Context, credID uuid.UUID) (*storageEntity.Credential, error)
	
	// [COMMENT]: Liệt kê danh sách các Keys đang hoạt động của Bucket.
	ListCredentials(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.Credential, error)
	
	// [COMMENT]: Thu hồi / Xóa bỏ Access Key (DB delete + MinIO revoke).
	RevokeCredential(ctx context.Context, credID uuid.UUID) error
}
