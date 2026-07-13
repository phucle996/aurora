package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalObjectService định nghĩa các nghiệp vụ xử lý thao tác với đối tượng lưu trữ cá nhân.
type PersonalObjectService interface {
	// [COMMENT]: Đăng ký job thao tác đối tượng lưu trữ cá nhân (list/upload/download/delete), trả về transaction_id (UUID)
	RegisterObjectPresign(ctx context.Context, param *storageEntity.RequestObjectPresignParam) (uuid.UUID, error)
}
