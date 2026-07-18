package dto

// [COMMENT]: ListTiersRequest đại diện cho các tham số lọc truy vấn danh sách Tier.
type ListTiersRequest struct {
	Page        int    `form:"page,default=1"`   // Số thứ tự trang hiện tại, mặc định là 1
	Limit       int    `form:"limit,default=10"` // Số lượng bản ghi trên một trang, mặc định là 10
	ServiceType string `form:"service_type"`     // Lọc theo loại dịch vụ (STORAGE | NETWORK_IN | NETWORK_OUT)
	Search      string `form:"search"`           // Tìm kiếm tương đối theo Name hoặc Code của Tier
}

// UpdateTierRangeRequest là một range trong full-state payload từ màn Edit.
type UpdateTierRangeRequest struct {
	ID            string `json:"id"`
	RangeStart    int64  `json:"range_start"`
	RangeEnd      int64  `json:"range_end"`
	BaseUnitPrice int64  `json:"base_unit_price"`
}

// UpdateTierRequest dùng code + service_type làm identity bất biến và version làm OCC token.
type UpdateTierRequest struct {
	Code        string                   `json:"code" binding:"required"`
	ServiceType string                   `json:"service_type" binding:"required"`
	Version     int                      `json:"version" binding:"required,min=1"`
	Name        string                   `json:"name" binding:"required"`
	Ranges      []UpdateTierRangeRequest `json:"ranges" binding:"required,min=1,dive"`
}
