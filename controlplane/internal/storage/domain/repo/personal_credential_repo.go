package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalCredentialRepo định nghĩa các phương thức giao tiếp CSDL cho Access Keys của Bucket cá nhân.
type PersonalCredentialRepo interface {
	// [COMMENT]: Lưu trữ Access Key mới liên kết với Bucket cá nhân cùng sự kiện Outbox.
	Create(ctx context.Context, param *storageEntity.CreatePersonalCredential, outbox *storageEntity.StorageOutboxRecord) (uuid.UUID, error)

	// [COMMENT]: Truy xuất thông tin Access Key theo ID.
	GetByID(ctx context.Context, id uuid.UUID) (*storageEntity.PersonalCredential, error)

	// [COMMENT]: Liệt kê toàn bộ Access Keys thuộc một Bucket cá nhân dưới dạng rút gọn (không gồm BucketID)
	ListByBucket(ctx context.Context, bucketID uuid.UUID) ([]*storageEntity.PersonalCredentialListItem, error)

	// [COMMENT]: Xóa thông tin Access Key khỏi Database cùng sự kiện Outbox (scoping theo struct params).
	Delete(ctx context.Context, param *storageEntity.DeletePersonalCredential, outbox *storageEntity.StorageOutboxRecord) error
}
