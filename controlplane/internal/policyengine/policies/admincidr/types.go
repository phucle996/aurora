// ============================================================================
// 📂 FILE: policies/admincidr/types.go - Định Nghĩa Mô Hình Cấu Hình Admin CIDR
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Định nghĩa cấu trúc dữ liệu YAML đầu vào (raw) và Compiled đầu ra (runtime)
//     cho tính năng giới hạn IP truy cập quản trị (Admin CIDR Control).
//   - Hỗ trợ phân tích dữ liệu có kiểu (strong-typed mapping) từ tệp `policy.yaml`.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Được ánh xạ trực tiếp từ trường `policies.admin_cidr` trong tệp cấu hình
//     động `runtime/policies/policy.yaml`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Xác định danh sách IP tin cậy (allowlist) được phép truy cập tài nguyên quản trị.
//   - Ngăn chặn triệt để các truy cập trái phép từ bên ngoài mạng WAN vào hệ thống nội bộ.
//
// 🔄 CALLSITE FLOW:
//   - Struct `AdminCIDRPolicy` được dùng bởi bộ phân tích cú pháp YAML (configyaml).
//   - Struct `CompiledPolicy` được lưu trong `RuntimePolicies` và truy cập bởi
//     `middleware/admin_cidr.go` tại mỗi HTTP Request.
//
// ============================================================================

package admincidr

// AdminCIDRPolicy đại diện cho cấu trúc thô (raw) được ánh xạ trực tiếp từ file YAML.
type AdminCIDRPolicy struct {
	// Enabled bật hoặc tắt cơ chế kiểm tra IP truy cập Admin.
	Enabled   bool     `yaml:"enabled"`
	
	// Mode xác định chế độ xử lý: "enforce" (chặn truy cập) hoặc "dryrun" (chỉ log cảnh báo).
	Mode      string   `yaml:"mode"`
	
	// Allowlist chứa danh sách các dải mạng IP hợp lệ dưới dạng CIDR (ví dụ: "127.0.0.1/32").
	Allowlist []string `yaml:"allowlist"`
}

// CompiledPolicy đại diện cho cấu hình đã được biên dịch và kiểm tra tính hợp lệ nghiêm ngặt.
// Các trường trong struct này là bất biến (read-only) trong suốt chu kỳ chạy của một snapshot chính sách.
type CompiledPolicy struct {
	// Enabled cho biết chính sách này có đang hoạt động hay không.
	Enabled   bool
	
	// Mode là chế độ thực thi đã được kiểm tra tính hợp lệ ("enforce" hoặc "dryrun").
	Mode      string
	
	// Allowlist chứa danh sách CIDR hợp lệ đã được kiểm duyệt cú pháp.
	Allowlist []string
}
