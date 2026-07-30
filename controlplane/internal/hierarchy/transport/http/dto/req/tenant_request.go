package dto

// [COMMENT]: CreateTenantRequest định nghĩa dữ liệu đầu vào JSON từ client để tạo Tenant
type CreateTenantRequest struct {
	// Name tên hiển thị của tổ chức (bắt buộc)
	Name string `json:"name" binding:"required"`
	// Code mã viết tắt định danh duy nhất (bắt buộc)
	Code string `json:"code" binding:"required"`
}
