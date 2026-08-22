package dto

import (
	"encoding/json"
	"time"
)

type CreateHypervisorZonePriceAdjustmentRequest struct {
	ExpectedLatestVersion json.Number `json:"expected_latest_version" binding:"required"`
	EffectiveFrom         time.Time   `json:"effective_from" binding:"required"`
	ChangeReason          string      `json:"change_reason" binding:"required"`
	MultiplierNumerator   string      `json:"multiplier_numerator" binding:"required"`
	MultiplierDenominator string      `json:"multiplier_denominator" binding:"required"`
}
