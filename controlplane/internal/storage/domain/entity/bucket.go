package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: BucketStatus định nghĩa các trạng thái vòng đời của một Storage Bucket.
type BucketStatus string

const (
	// [COMMENT]: Trạng thái khởi tạo, đang chờ setup vật lý trên MinIO cluster.
	BucketStatusCreating BucketStatus = "creating"
	// [COMMENT]: Hoạt động bình thường, cho phép đọc/ghi dữ liệu.
	BucketStatusActive   BucketStatus = "active"
	// [COMMENT]: Tạm ngưng hoạt động, chặn đọc/ghi nhưng không xóa dữ liệu.
	BucketStatusSuspended BucketStatus = "suspended"
	// [COMMENT]: Đã xóa, đang chờ dọn dẹp vật lý trên MinIO.
	BucketStatusDeleted   BucketStatus = "deleted"
)

// [COMMENT]: PersonalBucket đại diện cho thực thể nhóm lưu trữ dữ liệu cá nhân.
type PersonalBucket struct {
	ID                 uuid.UUID    // ID định danh duy nhất của bucket
	Name               string       // Tên bucket vật lý (phải unique toàn hệ thống)
	WorkspaceID        uuid.UUID    // ID của Workspace chứa bucket này
	ZoneID             uuid.UUID    // ID của Infrastructure Zone chứa bucket này
	Status             BucketStatus // Trạng thái hoạt động hiện tại
	CapacityQuotaBytes int64        // Hạn mức dung lượng lưu trữ tối đa (Bytes)
	CreatedAt          time.Time    // Thời gian tạo bản ghi
	UpdatedAt          time.Time    // Thời gian cập nhật bản ghi cuối
}

// [COMMENT]: TenantBucket đại diện cho thực thể nhóm lưu trữ dữ liệu doanh nghiệp.
type TenantBucket struct {
	ID                 uuid.UUID    // ID định danh duy nhất của bucket
	Name               string       // Tên bucket vật lý (phải unique toàn hệ thống)
	WorkspaceID        uuid.UUID    // ID của Workspace chứa bucket này
	ZoneID             uuid.UUID    // ID của Infrastructure Zone chứa bucket này
	TenantID           uuid.UUID    // ID của tổ chức doanh nghiệp sở hữu bucket (NOT NULL)
	Status             BucketStatus // Trạng thái hoạt động hiện tại
	CapacityQuotaBytes int64        // Hạn mức dung lượng lưu trữ tối đa (Bytes)
	CreatedAt          time.Time    // Thời gian tạo bản ghi
	UpdatedAt          time.Time    // Thời gian cập nhật bản ghi cuối
}
