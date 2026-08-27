package unit_test

import (
	"testing"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageProto "controlplane/internal/storage/transport/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestCommercialAdmissionZonePayloadEncoder(t *testing.T) {
	effectiveAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.FixedZone("source", 7*60*60))
	reason := "INSUFFICIENT_BALANCE"
	projection := &storageEntity.CommercialAdmissionZoneProjection{
		EventID:           uuid.MustParse("31ed91e8-f03c-4431-986f-11140384d1a2"),
		OwnerID:           uuid.MustParse("85ea38ed-91d0-4684-8ce8-6367c7d709f1"),
		OwnerType:         "PERSONAL",
		PolicyVersion:     12,
		Decision:          "SUSPEND_BILLABLE",
		RestrictionReason: &reason,
		EffectiveAt:       effectiveAt,
		ResourceID:        uuid.MustParse("b50ed940-0fe3-4b65-857c-1cc7469aa8f2"),
		ResourceName:      "ws-12345678-archive",
		ZoneID:            uuid.MustParse("ed7d23fe-c962-494a-aa9a-569598ce3131"),
	}

	payload, err := storageProto.NewCommercialAdmissionZonePayloadEncoder().Encode(projection)
	if err != nil {
		t.Fatalf("encode Zone payload: %v", err)
	}
	var event storageProto.StorageAdmissionChangedV1
	if err := proto.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode Zone payload: %v", err)
	}
	if event.EffectiveAt != "2026-08-16T03:00:00Z" {
		t.Fatalf("effective_at is not normalized to UTC: %q", event.EffectiveAt)
	}
	if event.ResourceId != projection.ResourceID.String() || event.ZoneId != projection.ZoneID.String() {
		t.Fatalf("unexpected Zone resource target: %#v", &event)
	}
}
