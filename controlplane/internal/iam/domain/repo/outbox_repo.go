package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// IamOutboxRepository định nghĩa interface thao tác dữ liệu với bảng outbox của module IAM.
type IamOutboxRepository interface {
	// Create tạo mới một bản ghi outbox trong database
	Create(ctx context.Context, record *iamEntity.IamOutboxRecord) error
	// FetchPendingForUpdate lấy danh sách các bản ghi outbox PENDING để xử lý
	FetchPendingForUpdate(ctx context.Context, limit int) ([]*iamEntity.IamOutboxRecord, error)
}
