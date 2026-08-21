package storageproto

import (
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"

	"google.golang.org/protobuf/proto"
)

type CommercialAdmissionZonePayloadEncoder struct{}

func NewCommercialAdmissionZonePayloadEncoder() storageRepoInterface.CommercialAdmissionZonePayloadEncoder {
	return &CommercialAdmissionZonePayloadEncoder{}
}

func (e *CommercialAdmissionZonePayloadEncoder) Encode(
	projection *storageEntity.CommercialAdmissionZoneProjection,
) ([]byte, error) {
	event := &StorageAdmissionChangedV1{
		EventId:       projection.EventID.String(),
		OwnerId:       projection.OwnerID.String(),
		OwnerType:     projection.OwnerType,
		PolicyVersion: projection.PolicyVersion,
		Decision:      projection.Decision,
		EffectiveAt:   projection.EffectiveAt.UTC().Format(time.RFC3339Nano),
		ResourceId:    projection.ResourceID.String(),
		ResourceName:  projection.ResourceName,
		ZoneId:        projection.ZoneID.String(),
	}
	if projection.RestrictionReason != nil {
		event.RestrictionReason = *projection.RestrictionReason
	}
	if projection.ValidUntil != nil {
		event.ValidUntil = projection.ValidUntil.UTC().Format(time.RFC3339Nano)
	}
	return proto.Marshal(event)
}
