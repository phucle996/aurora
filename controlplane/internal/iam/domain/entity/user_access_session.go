package iamEntity

// UserAccessSession đại diện cho trạng thái hoạt động thực tế (live runtime session) của thiết bị user trong Redis.
// Đã loại bỏ AccessKey và UserID vì chúng đã được định danh trực tiếp qua cấu trúc Redis key.
type UserAccessSession struct {
	// [COMMENT]: Đã loại bỏ AccessSecretHash và các json tags không cần thiết ở tầng domain entity này.
	TrackedDeviceID string // ID thiết bị được liên kết
	LastSeenAt      int64  // Lần cuối có request xác thực thành công (Unix timestamp, cập nhật bởi middleware)
}
