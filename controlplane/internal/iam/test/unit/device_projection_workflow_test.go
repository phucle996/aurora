package unit

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

func TestSelfDeviceServiceAppliesPresenceProjectionBatch(t *testing.T) {
	repository := &selfDeviceRepositoryStub{}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewSelfDeviceService(repository, nil, nil, nil, metrics)
	updates := []iamEntity.DevicePresenceUpdate{{
		DeviceID:          uuid.NewString(),
		LastSeenAt:        1_786_594_400,
		LastSeenIP:        "203.0.113.10",
		LastSeenUserAgent: "Aurora Console",
	}}

	if err := service.ApplyDevicePresenceProjection(context.Background(), updates); err != nil {
		t.Fatalf("apply presence projection: %v", err)
	}
	if len(repository.updates) != 1 || repository.updates[0] != updates[0] {
		t.Fatalf("repository received unexpected presence batch: %#v", repository.updates)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultSuccess || metrics.reason != observability.ReasonNone {
		t.Fatalf("unexpected presence workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}

func TestSelfDeviceServicePropagatesCapacityEvictionFailure(t *testing.T) {
	repository := &selfDeviceRepositoryStub{err: errors.New("postgres unavailable")}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewSelfDeviceService(repository, nil, nil, nil, metrics)
	userID := uuid.New()
	deviceID := uuid.New()

	err := service.ApplyDeviceSessionCapacityEviction(context.Background(), userID, []uuid.UUID{deviceID})
	if !errors.Is(err, repository.err) {
		t.Fatalf("expected durable failure to stay retryable, got %v", err)
	}
	if repository.evictionUser != userID || len(repository.evictionIDs) != 1 || repository.evictionIDs[0] != deviceID {
		t.Fatalf("repository received unexpected eviction: user=%s devices=%v", repository.evictionUser, repository.evictionIDs)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultFailure || metrics.reason != observability.ReasonInternal {
		t.Fatalf("unexpected capacity workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}
