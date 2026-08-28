package iamEntity

import "github.com/google/uuid"

// [COMMENT]: TenantRenderContext là thực thể nghiệp vụ mang dữ liệu cho workflow
// đọc render context trong ngữ cảnh Tenant cụ thể. Tách biệt hoàn toàn với PersonalRenderContext
// để đảm bảo tính cô lập (boundary isolation) và không bao giờ rò rỉ quyền hạn chéo.
type TenantRenderContext struct {
	UserID            uuid.UUID // ID của user đang đăng nhập trong tenant session
	TenantID          uuid.UUID // ID của tenant tổ chức mà user đang truy cập
	Permissions       []string  // Danh sách quyền 5 cấp từ membership_role đã compile
	Capabilities      []string  // Danh sách capabilities tenant đã chuẩn hóa và sắp xếp
	NavigationKeys    []string  // Các navigation keys phục vụ vẽ menu tenant
	NavigationActions []string  // Các hành động tương ứng cho từng menu item tenant
}
