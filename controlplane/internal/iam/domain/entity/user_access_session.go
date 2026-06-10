package iamEntity

// UserAccessSession đại diện cho trạng thái hoạt động thực tế (live runtime session) của thiết bị user trong Redis.
// Đã loại bỏ AccessKey và UserID vì chúng đã được định danh trực tiếp qua cấu trúc Redis key.
type UserAccessSession struct {
	AccessSecretHash string `json:"ash"`  // Hash SHA-256 của Access secret để so khớp chữ ký
	TrackedDeviceID  string `json:"tdid"` // ID thiết bị được liên kết
	LastSeenAt       int64  `json:"lsa"`  // Lần cuối có request xác thực thành công (Unix timestamp, cập nhật bởi middleware)
}
