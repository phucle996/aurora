package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalCredential đại diện cho thông tin tài khoản Access Keys truy cập MinIO Bucket cá nhân.
type PersonalCredential struct {
	ID        uuid.UUID // ID định danh duy nhất của credentials
	BucketID  uuid.UUID // ID của bucket cá nhân liên kết
	AccessKey string    // Access Key của tài khoản MinIO
	SecretKey string    // Secret Key (mã hóa)
	Policy    string    // Phân quyền chi tiết (JSON policy)
	CreatedAt time.Time // Thời gian khởi tạo bản ghi
	UpdatedAt time.Time // Thời gian cập nhật bản ghi
}

// [COMMENT]: TenantCredential đại diện cho thông tin tài khoản Access Keys truy cập MinIO Bucket doanh nghiệp.
type TenantCredential struct {
	ID        uuid.UUID // ID định danh duy nhất của credentials
	BucketID  uuid.UUID // ID của bucket doanh nghiệp liên kết
	AccessKey string    // Access Key của tài khoản MinIO
	SecretKey string    // Secret Key (mã hóa)
	Policy    string    // Phân quyền chi tiết (JSON policy)
	CreatedAt time.Time // Thời gian khởi tạo bản ghi
	UpdatedAt time.Time // Thời gian cập nhật bản ghi
}

// [COMMENT]: CreatePersonalCredential chứa các tham số dùng để khởi tạo Access Key cá nhân.
type CreatePersonalCredential struct {
	BucketID uuid.UUID
	Policy   string
	UserID   uuid.UUID
}

// [COMMENT]: CreateTenantCredential chứa các tham số dùng để khởi tạo Access Key doanh nghiệp.
type CreateTenantCredential struct {
	BucketID uuid.UUID
	Policy   string
	UserID   uuid.UUID
}
