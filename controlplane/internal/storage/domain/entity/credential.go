package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalCredential đại diện cho thông tin tài khoản Access Keys truy cập MinIO Bucket cá nhân (đã loại bỏ SecretKey khỏi thực thể lõi).
type PersonalCredential struct {
	ID        uuid.UUID // ID định danh duy nhất của credentials
	BucketID  uuid.UUID // ID của bucket cá nhân liên kết
	AccessKey string    // Access Key của tài khoản MinIO
	Policy    string    // Phân quyền chi tiết (JSON policy)
	CreatedAt time.Time // Thời gian khởi tạo bản ghi
	UpdatedAt time.Time // Thời gian cập nhật bản ghi
}

// [COMMENT]: TenantCredential đại diện cho thông tin tài khoản Access Keys truy cập MinIO Bucket doanh nghiệp (đã loại bỏ SecretKey khỏi thực thể lõi).
type TenantCredential struct {
	ID        uuid.UUID // ID định danh duy nhất của credentials
	BucketID  uuid.UUID // ID của bucket doanh nghiệp liên kết
	AccessKey string    // Access Key của tài khoản MinIO
	Policy    string    // Phân quyền chi tiết (JSON policy)
	CreatedAt time.Time // Thời gian khởi tạo bản ghi
	UpdatedAt time.Time // Thời gian cập nhật bản ghi
}

// [COMMENT]: CreatedPersonalCredential đại diện cho kết quả khởi tạo Access Key cá nhân, chứa raw Secret Key phản hồi 1 lần duy nhất.
type CreatedPersonalCredential struct {
	ID        uuid.UUID
	BucketID  uuid.UUID
	AccessKey string
	SecretKey string // Plaintext Secret Key
	Policy    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: CreatePersonalCredential chứa các tham số dùng để khởi tạo Access Key cá nhân.
type CreatePersonalCredential struct {
	ID          uuid.UUID
	BucketName  string
	AccessKey   string
	Policy      string
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
}

// [COMMENT]: CreateTenantCredential chứa các tham số dùng để khởi tạo Access Key doanh nghiệp.
type CreateTenantCredential struct {
	BucketID    uuid.UUID
	Policy      string
	TenantID    uuid.UUID
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
}

// [COMMENT]: PersonalCredentialListItem chứa thông tin rút gọn cho danh sách credentials, tối ưu hóa không bao gồm BucketID.
type PersonalCredentialListItem struct {
	ID        uuid.UUID
	AccessKey string
	Policy    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: CreatedTenantCredential đại diện cho kết quả khởi tạo Access Key doanh nghiệp, chứa raw Secret Key phản hồi 1 lần duy nhất.
type CreatedTenantCredential struct {
	ID        uuid.UUID
	BucketID  uuid.UUID
	AccessKey string
	SecretKey string // Plaintext Secret Key
	Policy    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: DeletePersonalCredential chứa các tham số dùng để xóa Access Key cá nhân có check chéo scope.
type DeletePersonalCredential struct {
	CredentialID uuid.UUID
	AccessKey    string // [COMMENT]: Access Key của MinIO user cần xóa — không cần query DB thêm
	BucketID     uuid.UUID
	WorkspaceID  uuid.UUID
	UserID       uuid.UUID
	ZoneID       uuid.UUID // [COMMENT]: Lấy từ request context đã xác minh và ghi trực tiếp vào Outbox
}

// [COMMENT]: DeleteTenantCredential chứa các tham số dùng để xóa Access Key doanh nghiệp có check chéo scope.
type DeleteTenantCredential struct {
	CredentialID uuid.UUID
	AccessKey    string // [COMMENT]: Access Key của MinIO user cần xóa — không cần query DB thêm
	BucketID     uuid.UUID
	WorkspaceID  uuid.UUID
	TenantID     uuid.UUID
	UserID       uuid.UUID
	ZoneID       uuid.UUID // [COMMENT]: Lấy từ request context đã xác minh và ghi trực tiếp vào Outbox
}
