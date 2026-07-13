package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: PersonalObjectRepo định nghĩa các phương thức giao tiếp CSDL để cấp quyền ký cho các đối tượng (Objects).
type PersonalObjectRepo interface {
	// [COMMENT]: Khởi tạo job thao tác Object thông qua Outbox Record (list/upload/download/delete), có kiểm tra quyền sở hữu bucket (chống IDOR).
	CreateObjectPresign(ctx context.Context, param *storageEntity.RequestObjectPresignParam, outbox *storageEntity.StorageOutboxRecord) error
}
