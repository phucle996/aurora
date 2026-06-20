package iamEntity

import "time"

// TrinityRefreshResult chứa bộ trinity mới được cấp khi sliding session refresh thành công.
// Đây là kết quả của cơ chế Kiểu 1 — gia hạn phiên đang hoạt động.
type TrinityRefreshResult struct {
	AccessToken     string    // JWT access token mới, ký bằng runtime signing secret
	AccessKey       string    // Định danh phiên mới, dùng làm Redis key + cookie
	AccessSecret    string    // Bí mật phiên mới, hash SHA256 lưu trong Redis để middleware đối sánh
	TrackedDeviceID string    // ID thiết bị theo dõi, giữ nguyên từ phiên cũ
	AccessExpiresAt time.Time // Thời điểm hết hạn của access token mới
}
