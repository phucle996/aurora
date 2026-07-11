package storageRepoInterface

import (
	"context"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: PersonalBucketRepo định nghĩa các phương thức giao tiếp CSDL cho Bucket Cá nhân (Individual).
type PersonalBucketRepo interface {
	// [COMMENT]: Tạo mới Bucket + Credential + Outbox Record trong một atomic CTE duy nhất.
	Create(ctx context.Context, bucket *storageEntity.PersonalBucket, credential *storageEntity.PersonalCredential, outbox *storageEntity.StorageOutboxRecord) error
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo ID kèm theo userID để validate.
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Tìm kiếm thông tin chi tiết của một Bucket theo tên vật lý.
	GetByName(ctx context.Context, name string) (*storageEntity.PersonalBucket, error)

	// [COMMENT]: Liệt kê các Bucket thuộc sở hữu của một Workspace cụ thể trong một Zone kèm theo userID để validate.
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID) ([]*storageEntity.PersonalBucket, error)
	
	// [COMMENT]: Cập nhật trạng thái vận hành của Bucket (Active, Suspended, Deleted) có check quyền sở hữu của user.
	UpdateStatus(ctx context.Context, id uuid.UUID, userID uuid.UUID, status storageEntity.BucketStatus) error
	
	// [COMMENT]: Cập nhật hạn mức lưu trữ quota của Bucket có check quyền sở hữu của user.
	UpdateQuota(ctx context.Context, id uuid.UUID, userID uuid.UUID, quotaBytes int64) error
	
	// [COMMENT]: Xóa vĩnh viễn bản ghi Bucket ra khỏi Database có check quyền sở hữu của user.
	Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
}
