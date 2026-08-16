package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

type CommercialAdmissionProjectionCommand struct {
	EventID           uuid.UUID
	OwnerID           uuid.UUID
	OwnerType         string
	PolicyVersion     int64
	Decision          string
	RestrictionReason string
	EffectiveAt       time.Time
	ValidUntil        *time.Time
}

type CommercialAdmissionProjection struct {
	EventID           uuid.UUID
	OwnerID           uuid.UUID
	OwnerType         string
	PolicyVersion     int64
	Decision          string
	RestrictionReason *string
	EffectiveAt       time.Time
	ValidUntil        *time.Time
}

type CommercialAdmissionZoneProjection struct {
	EventID           uuid.UUID
	OwnerID           uuid.UUID
	OwnerType         string
	PolicyVersion     int64
	Decision          string
	RestrictionReason *string
	EffectiveAt       time.Time
	ValidUntil        *time.Time
	ResourceID        uuid.UUID
	ResourceName      string
	ZoneID            uuid.UUID
}
