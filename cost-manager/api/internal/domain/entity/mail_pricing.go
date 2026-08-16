package entity

import (
	"time"

	"github.com/google/uuid"
)

const ChargeKindMailAcceptedRecipient ChargeKindCode = "mail.delivery.accepted_recipient"

type MailZoneAdjustmentPublishCommand struct {
	ZoneID                uuid.UUID
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type MailZoneAdjustmentPublished struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type MailZoneAdjustmentSnapshot struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// MailEstimate is the flat output of the accepted-recipient estimate workflow.
type MailEstimate struct {
	RecipientQuantity         int64
	EstimateMicroUnits        int64
	Currency                  string
	PricingScheduleCode       string
	PricingScheduleID         uuid.UUID
	PricingScheduleVersionID  uuid.UUID
	PricingVersion            int
	PricingChecksum           string
	PricingEffectiveFrom      time.Time
	RateAdjustmentID          *uuid.UUID
	RateAdjustmentVersion     *int
	RateAdjustmentChecksum    *string
	RateAdjustmentNumerator   int64
	RateAdjustmentDenominator int64
	EstimatedAt               time.Time
}
