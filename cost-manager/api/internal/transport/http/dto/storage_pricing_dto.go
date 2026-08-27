package dto

import (
	"encoding/json"
	"time"
)

type CreateStorageBasePriceVersionRequest struct {
	ExpectedLatestVersion json.Number                  `json:"expected_latest_version" binding:"required"`
	EffectiveFrom         time.Time                    `json:"effective_from" binding:"required"`
	ChangeReason          string                       `json:"change_reason" binding:"required"`
	Brackets              []CreateScalarBracketRequest `json:"brackets" binding:"required,min=1,dive"`
}

type CreateHypervisorBasePriceVersionRequest struct {
	ExpectedLatestVersion json.Number                  `json:"expected_latest_version" binding:"required"`
	EffectiveFrom         time.Time                    `json:"effective_from" binding:"required"`
	ChangeReason          string                       `json:"change_reason" binding:"required"`
	Brackets              []CreateScalarBracketRequest `json:"brackets" binding:"required,min=1,dive"`
}

type CreateMailBasePriceVersionRequest struct {
	ExpectedLatestVersion json.Number                  `json:"expected_latest_version" binding:"required"`
	EffectiveFrom         time.Time                    `json:"effective_from" binding:"required"`
	ChangeReason          string                       `json:"change_reason" binding:"required"`
	Brackets              []CreateScalarBracketRequest `json:"brackets" binding:"required,min=1,dive"`
}
