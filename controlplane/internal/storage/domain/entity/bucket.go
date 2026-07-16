package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalBucket đại diện cho thực thể nhóm lưu trữ dữ liệu cá nhân.
// Status đã bị loại bỏ — bucket hoặc tồn tại (ready) hoặc không (đã xóa khi tạo thất bại).
type PersonalBucket struct {
	ID                   uuid.UUID // ID định danh duy nhất của bucket
	Name                 string    // Tên bucket vật lý (phải unique toàn hệ thống)
	ZoneID               uuid.UUID // ID of Infrastructure Zone containing this bucket
	CapacityQuotaBytes   int64     // Hạn mức dung lượng lưu trữ tối đa (Bytes)
	UsedBytes            int64     // Hạn mức dung lượng lưu trữ hiện tại đang sử dụng (Bytes)
	CreatedAt            time.Time // Thời gian tạo bản ghi
	UpdatedAt            time.Time // Thời gian cập nhật bản ghi cuối
	EncryptEnabled       bool
	VersioningEnabled     bool
	ObjectLockingEnabled bool
	ReplicationEnabled   bool
	RetentionDays        int64
	LegalHoldEnabled     bool
	Tags                 map[string]string
}

// [COMMENT]: TenantBucket đại diện cho thực thể nhóm lưu trữ dữ liệu doanh nghiệp.
// Status đã bị loại bỏ — bucket hoặc tồn tại (ready) hoặc không (đã xóa khi tạo thất bại).
type TenantBucket struct {
	ID                   uuid.UUID // ID định danh duy nhất của bucket
	Name                 string    // Tên bucket vật lý (phải unique toàn hệ thống)
	WorkspaceID          uuid.UUID // ID của Workspace chứa bucket này
	ZoneID               uuid.UUID // ID của Infrastructure Zone chứa bucket này
	TenantID             uuid.UUID // ID của tổ chức doanh nghiệp sở hữu bucket (NOT NULL)
	CapacityQuotaBytes   int64     // Hạn mức dung lượng lưu trữ tối đa (Bytes)
	UsedBytes            int64     // Hạn mức dung lượng lưu trữ hiện tại đang sử dụng (Bytes)
	CreatedAt            time.Time // Thời gian tạo bản ghi
	UpdatedAt            time.Time // Thời gian cập nhật bản ghi cuối
	EncryptEnabled       bool
	VersioningEnabled     bool
	ObjectLockingEnabled bool
	ReplicationEnabled   bool
	RetentionDays        int64
	LegalHoldEnabled     bool
	Tags                 map[string]string
}

// [COMMENT]: CreatePersonalBucket chứa các tham số dùng để khởi tạo Bucket cá nhân.
type CreatePersonalBucket struct {
	Name                 string
	WorkspaceID          uuid.UUID
	ZoneID               uuid.UUID
	CapacityQuotaBytes   int64
	UserID               uuid.UUID
	Policy               string
	EncryptEnabled       bool
	VersioningEnabled     bool
	ObjectLockingEnabled bool
	ReplicationEnabled   bool
	RetentionDays        int64
	LegalHoldEnabled     bool
	Tags                 map[string]string
}

// [COMMENT]: CreateTenantBucket chứa các tham số dùng để khởi tạo Bucket doanh nghiệp.
type CreateTenantBucket struct {
	Name                 string
	WorkspaceID          uuid.UUID
	ZoneID               uuid.UUID
	TenantID             uuid.UUID
	CapacityQuotaBytes   int64
	UserID               uuid.UUID
	EncryptEnabled       bool
	VersioningEnabled     bool
	ObjectLockingEnabled bool
	ReplicationEnabled   bool
	RetentionDays        int64
	LegalHoldEnabled     bool
	Tags                 map[string]string
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

// [COMMENT]: DeletePersonalBucket chứa thông tin tham số để thực hiện xóa bucket cá nhân và credentials liên quan.
type DeletePersonalBucket struct {
	BucketID    uuid.UUID
	BucketName  string
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	UserID      uuid.UUID
}

// [COMMENT]: DeleteTenantBucket chứa thông tin tham số để thực hiện xóa bucket doanh nghiệp và credentials liên quan.
type DeleteTenantBucket struct {
	BucketID    uuid.UUID
	BucketName  string
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
	UserID      uuid.UUID
}

// [COMMENT]: RequestBucketSts chứa các tham số dùng để gửi yêu cầu xin cấp STS token cho bucket.
type RequestBucketSts struct {
	BucketID        uuid.UUID
	BucketName      string
	DurationSeconds int64
	UserID          uuid.UUID
	WorkspaceID     uuid.UUID
	ZoneID          uuid.UUID
}
