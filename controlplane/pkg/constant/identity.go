// ============================================================================
// 📂 FILE: controlplane/pkg/constant/identity.go - Shared Auth Context Type
// ============================================================================
// Định nghĩa thực thể Identity dùng chung để trao đổi thông tin định danh
// giữa Middleware và Service layer qua Go standard context.
// ============================================================================

package constant

// IdentityKeyType là kiểu dữ liệu riêng cho context key, triệt tiêu nguy cơ
// xung đột khóa (context key collision) giữa các thư viện hoặc phân hệ khác nhau.
type IdentityKeyType struct{}

// IdentityKey là instance duy nhất dùng làm key trong context.WithValue()
var IdentityKey = IdentityKeyType{}

// Identity lưu trữ tất cả thông tin liên quan đến định danh người dùng/thiết bị
// đã xác thực trong phiên làm việc hiện tại.
// Bằng cách gộp toàn bộ vào một struct duy nhất, ta tránh được việc tạo chuỗi
// ValueContext lồng nhau quá sâu, giúp tối ưu hóa bộ nhớ và tăng tốc độ tra cứu O(1).
type Identity struct {
	// UserID chứa UUID (ở dạng chuỗi) của người dùng hoặc định danh đặc biệt (vd: "sre")
	UserID string

	// Role là vai trò phân quyền hệ thống (vd: "admin", "tenant_owner", "member")
	Role string

	// TenantID là ID phân vùng tổ chức của user hiện tại
	TenantID string

	// Level là cấp độ đặc quyền đặc biệt phục vụ phân quyền RBAC
	Level int

	// ZoneID là phân vùng địa lý/hạ tầng mà người dùng hoặc Admin bị giới hạn truy cập
	ZoneID string

	// AccessKey là mã định danh phiên chạy runtime (Session Access Key)
	AccessKey string

	// AccessSecret là khóa bảo mật phiên chạy runtime (Session Access Secret)
	AccessSecret string

	// TrackedDeviceID là ID thiết bị vật lý đã được liên kết và đăng ký
	TrackedDeviceID string

	// JTI là mã JWT Token ID duy nhất dùng chống Replay Attack
	JTI string
}
