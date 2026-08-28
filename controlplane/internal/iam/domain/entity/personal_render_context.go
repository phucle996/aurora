package iamEntity

import "github.com/google/uuid"

// [COMMENT]: PersonalRenderContext là thực thể nghiệp vụ duy nhất mang dữ liệu cho workflow
// đọc render context cấp platform / cá nhân. Thực thể này chứa toàn bộ quyền đã biên dịch
// cùng danh sách capabilities và navigation đã deduplicate để UI frontend render menu.
type PersonalRenderContext struct {
	UserID            uuid.UUID // ID của user đang đăng nhập trong platform session
	Permissions       []string  // Danh sách quyền thô 5 cấp đã compile từ L1/L2 (user_role)
	Capabilities      []string  // Danh sách capabilities đã chuẩn hóa và sắp xếp
	NavigationKeys    []string  // Các module/object key phục vụ render thanh điều hướng (menu items)
	NavigationActions []string  // Các hành động tương ứng cho từng navigation key (vd: read, manage)
}

