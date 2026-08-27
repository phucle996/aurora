package storageEntity

import "github.com/google/uuid"

type CommercialAdmissionZoneDelivery struct {
	ResourceID    uuid.UUID
	ZoneID        uuid.UUID
	SourceEventID uuid.UUID
	PolicyVersion int64
	Payload       []byte
	ClaimToken    uuid.UUID
}
