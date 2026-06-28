package coreReq

// [COMMENT]: CreateWorkspaceRequest định nghĩa dữ liệu đầu vào từ HTTP body để tạo workspace
type CreateWorkspaceRequest struct {
	// Name tên hiển thị của workspace (bắt buộc)
	Name string `json:"name" binding:"required"`
	// Code mã viết tắt định danh duy nhất (bắt buộc)
	Code string `json:"code" binding:"required"`
}
