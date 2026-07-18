package dto

import "time"

// [COMMENT]: ListTiersRequest đại diện cho các tham số lọc truy vấn danh sách Tier.
type ListTiersRequest struct {
	Page        int    `form:"page,default=1"`   // Số thứ tự trang hiện tại, mặc định là 1
	Limit       int    `form:"limit,default=10"` // Số lượng bản ghi trên một trang, mặc định là 10
	ServiceType string `form:"service_type"`     // Lọc theo loại dịch vụ (STORAGE | NETWORK_IN | NETWORK_OUT | VM)
	Search      string `form:"search"`           // Tìm kiếm tương đối theo Name hoặc Code của Tier
}

// CreateTierVersionRangeRequest là một range mới trong immutable pricing snapshot.
type CreateTierVersionRangeRequest struct {
	RangeStart    int64 `json:"range_start"`
	RangeEnd      int64 `json:"range_end"`
	BaseUnitPrice int64 `json:"base_unit_price"`
}

// UpdateTierMetadataRequest sửa name với metadata OCC, không đụng pricing history.
type UpdateTierMetadataRequest struct {
	MetadataVersion int    `json:"metadata_version" binding:"required,min=1"`
	Name            string `json:"name" binding:"required"`
}

// CreateTierVersionRequest append một full pricing snapshot và lịch hiệu lực.
type CreateTierVersionRequest struct {
	ExpectedLatestVersion int                             `json:"expected_latest_version" binding:"required,min=1"`
	EffectiveFrom         time.Time                       `json:"effective_from" binding:"required"`
	ChangeReason          string                          `json:"change_reason" binding:"required"`
	Ranges                []CreateTierVersionRangeRequest `json:"ranges" binding:"required,min=1,dive"`
}
