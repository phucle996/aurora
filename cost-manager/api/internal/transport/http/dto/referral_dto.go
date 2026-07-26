package dto

// [COMMENT]: ReserveReferralRequest DTO cho payload đăng ký mã giới thiệu
type ReserveReferralRequest struct {
	Code string `json:"code" binding:"required"`
}

// [COMMENT]: CreateReferralCampaignRequest DTO cho payload tạo mới chiến dịch giới thiệu
type CreateReferralCampaignRequest struct {
	Code                   string  `json:"code" binding:"required"`
	Name                   string  `json:"name" binding:"required"`
	AmountMicroUnits       string  `json:"amount_micro_units" binding:"required"`
	MinimumTopUpMicroUnits string  `json:"minimum_top_up_micro_units" binding:"required"`
	Currency               string  `json:"currency" binding:"required"`
	MaxRedemptions         *string `json:"max_redemptions"`
	StartsAt               string  `json:"starts_at" binding:"required"`
	EndsAt                 *string `json:"ends_at"`
}

// [COMMENT]: UpdateReferralCampaignStatusRequest DTO cho payload cập nhật trạng thái chiến dịch giới thiệu
type UpdateReferralCampaignStatusRequest struct {
	Status          string `json:"status" binding:"required"`
	ExpectedVersion string `json:"expected_version" binding:"required"`
}
