package hierarchyReq

// [COMMENT]: CreatePersonalWorkspaceRequest định nghĩa dữ liệu đầu vào từ HTTP body để tạo workspace cá nhân.
// Scope personal không có tenant_id — ZoneID được lấy từ header do ACR inject.
type CreatePersonalWorkspaceRequest struct {
	// Name tên hiển thị của workspace (bắt buộc)
	Name string `json:"name" binding:"required"`
	// Code mã viết tắt định danh duy nhất (bắt buộc)
	Code string `json:"code" binding:"required"`
	// Description mô tả thêm về workspace (không bắt buộc)
	Description string `json:"description"`
}

// [COMMENT]: CreateTenantWorkspaceRequest định nghĩa dữ liệu đầu vào từ HTTP body để tạo workspace thuộc tenant.
// TenantID được lấy từ header x-tenant-id do ACR inject, không cần khai báo trong body.
type CreateTenantWorkspaceRequest struct {
	// Name tên hiển thị của workspace (bắt buộc)
	Name string `json:"name" binding:"required"`
	// Code mã viết tắt định danh duy nhất (bắt buộc)
	Code string `json:"code" binding:"required"`
	// Description mô tả thêm về workspace (không bắt buộc)
	Description string `json:"description"`
}
