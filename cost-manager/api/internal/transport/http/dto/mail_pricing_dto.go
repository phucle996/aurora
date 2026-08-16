package dto

import "time"

type CreateMailZonePriceAdjustmentRequest struct {
	ExpectedLatestVersion int       `json:"expected_latest_version"`
	EffectiveFrom         time.Time `json:"effective_from" binding:"required"`
	ChangeReason          string    `json:"change_reason" binding:"required"`
	MultiplierNumerator   string    `json:"multiplier_numerator" binding:"required"`
	MultiplierDenominator string    `json:"multiplier_denominator" binding:"required"`
}
