package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Credential đại diện cho thông tin tài khoản Access Keys truy cập MinIO Bucket.
type Credential struct {
	ID        uuid.UUID // ID định danh duy nhất của credentials
	BucketID  uuid.UUID // ID của bucket liên kết
	AccessKey string    // Access Key của tài khoản MinIO
	SecretKey string    // Secret Key (được lưu ở dạng mã hóa KMS/Vault)
	Policy    string    // Phân quyền chi tiết (JSON policy, ví dụ ReadOnly, ReadWrite)
	CreatedAt time.Time // Thời gian khởi tạo bản ghi
	UpdatedAt time.Time // Thời gian cập nhật bản ghi
}
