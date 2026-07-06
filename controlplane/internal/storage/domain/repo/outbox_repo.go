package storageRepoInterface

import (
	"context"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: StorageOutboxRepository định nghĩa interface tương tác dữ liệu với bảng outbox của module Storage.
type StorageOutboxRepository interface {
	// [COMMENT]: Tạo mới một bản ghi outbox job trong database.
	Create(ctx context.Context, record *storageEntity.StorageOutboxRecord) error
}
