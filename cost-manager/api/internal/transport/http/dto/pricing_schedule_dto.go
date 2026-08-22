package dto

import (
	"encoding/json"
	"time"
)

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

type CreateStorageZonePriceAdjustmentRequest struct {
	ExpectedLatestVersion json.Number `json:"expected_latest_version" binding:"required"`
	EffectiveFrom         time.Time   `json:"effective_from" binding:"required"`
	ChangeReason          string      `json:"change_reason" binding:"required"`
	MultiplierNumerator   string      `json:"multiplier_numerator" binding:"required"`
	MultiplierDenominator string      `json:"multiplier_denominator" binding:"required"`
}
