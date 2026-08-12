package dto

import "time"

type ListPricingSchedulesRequest struct {
	Page       int    `form:"page,default=1"`
	Limit      int    `form:"limit,default=10"`
	ChargeKind string `form:"charge_kind"`
	Search     string `form:"search"`
}

type CreateScalarBracketRequest struct {
	RangeStartQuantity       int64  `json:"range_start_quantity"`
	RangeEndQuantity         *int64 `json:"range_end_quantity"`
	PriceNumeratorMicroUnits int64  `json:"price_numerator_micro_units"`
	PriceDenominatorQuantity int64  `json:"price_denominator_quantity"`
}

type UpdatePricingScheduleMetadataRequest struct {
	MetadataVersion int    `json:"metadata_version" binding:"required,min=1"`
	DisplayName     string `json:"display_name" binding:"required"`
}

type CreatePricingScheduleVersionRequest struct {
	ExpectedLatestVersion int                          `json:"expected_latest_version" binding:"required,min=1"`
	EffectiveFrom         time.Time                    `json:"effective_from" binding:"required"`
	ChangeReason          string                       `json:"change_reason" binding:"required"`
	Brackets              []CreateScalarBracketRequest `json:"brackets" binding:"required,min=1,dive"`
}
