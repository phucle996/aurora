package dto

import "time"

type ListPricingSchedulesRequest struct {
	Page       int    `form:"page,default=1"`
	Limit      int    `form:"limit,default=10"`
	ChargeKind string `form:"charge_kind"`
	Search     string `form:"search"`
}

type CreateScalarBracketRequest struct {
	RangeStartQuantity       string  `json:"range_start_quantity" binding:"required"`
	RangeEndQuantity         *string `json:"range_end_quantity"`
	PriceNumeratorMicroUnits string  `json:"price_numerator_micro_units" binding:"required"`
	PriceDenominatorQuantity string  `json:"price_denominator_quantity" binding:"required"`
}

type UpdatePricingScheduleMetadataRequest struct {
	MetadataVersion int    `json:"metadata_version" binding:"required,min=1"`
	DisplayName     string `json:"display_name" binding:"required"`
}

type CreatePricingScheduleVersionRequest struct {
	ExpectedLatestVersion *int                         `json:"expected_latest_version" binding:"required,min=0"`
	EffectiveFrom         time.Time                    `json:"effective_from" binding:"required"`
	ChangeReason          string                       `json:"change_reason" binding:"required"`
	Brackets              []CreateScalarBracketRequest `json:"brackets" binding:"required,min=1,dive"`
}

type CreateStorageZonePriceAdjustmentRequest struct {
	ExpectedLatestVersion int       `json:"expected_latest_version"`
	EffectiveFrom         time.Time `json:"effective_from" binding:"required"`
	ChangeReason          string    `json:"change_reason" binding:"required"`
	MultiplierNumerator   string    `json:"multiplier_numerator" binding:"required"`
	MultiplierDenominator string    `json:"multiplier_denominator" binding:"required"`
}
