package dto

// [COMMENT]: ListTiersRequest đại diện cho các tham số lọc truy vấn danh sách Tier.
type ListTiersRequest struct {
	Page        int    `form:"page,default=1"`   // Số thứ tự trang hiện tại, mặc định là 1
	Limit       int    `form:"limit,default=10"` // Số lượng bản ghi trên một trang, mặc định là 10
	ServiceType string `form:"service_type"`     // Lọc theo loại dịch vụ (STORAGE | NETWORK_IN | NETWORK_OUT)
	Search      string `form:"search"`           // Tìm kiếm tương đối theo Name hoặc Code của Tier
}
