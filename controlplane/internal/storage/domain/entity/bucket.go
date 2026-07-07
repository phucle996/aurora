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

// [COMMENT]: CreatePersonalBucket chứa các tham số dùng để khởi tạo Bucket cá nhân.
type CreatePersonalBucket struct {
	Name               string
	WorkspaceID        uuid.UUID
	ZoneID             uuid.UUID
	CapacityQuotaBytes int64
	UserID             uuid.UUID
}

// [COMMENT]: CreateTenantBucket chứa các tham số dùng để khởi tạo Bucket doanh nghiệp.
type CreateTenantBucket struct {
	Name               string
	WorkspaceID        uuid.UUID
	ZoneID             uuid.UUID
	TenantID           uuid.UUID
	CapacityQuotaBytes int64
	UserID             uuid.UUID
}

// [COMMENT]: CreatedBucketResult mang thông tin bucket và credential vừa được tạo,
// trả về cho HTTP handler để phản hồi cho user một lần duy nhất.
type CreatedBucketResult struct {
	BucketID     uuid.UUID // ID bucket vừa được tạo
	BucketName   string    // Tên bucket
	CredentialID uuid.UUID // ID của credential gắn kèm
	AccessKey    string    // Access Key (plain) trả về user
	SecretKey    string    // Secret Key (plain) trả về user — chỉ hiển thị 1 lần duy nhất
	Policy       string    // JSON bucket policy được áp dụng
}
