package unit

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"

	"github.com/google/uuid"
)

type devicePresenceProjectionRepositoryStub struct {
	updates []iamEntity.DevicePresenceUpdate
	err     error
}

func (r *devicePresenceProjectionRepositoryStub) Apply(
	_ context.Context,
	updates []iamEntity.DevicePresenceUpdate,
) error {
	r.updates = updates
	return r.err
}

type deviceSessionCapacityEvictionRepositoryStub struct {
	userID    uuid.UUID
	deviceIDs []uuid.UUID
	err       error
}

func (r *deviceSessionCapacityEvictionRepositoryStub) Evict(
	_ context.Context,
	userID uuid.UUID,
	deviceIDs []uuid.UUID,
) error {
	r.userID = userID
	r.deviceIDs = deviceIDs
	return r.err
}

func TestDevicePresenceProjectionServiceAppliesNormalizedBatch(t *testing.T) {
	repository := &devicePresenceProjectionRepositoryStub{}
	service := iamService.NewDevicePresenceProjectionService(repository)
	updates := []iamEntity.DevicePresenceUpdate{{
		DeviceID:          uuid.NewString(),
		LastSeenAt:        1_786_594_400,
		LastSeenIP:        "203.0.113.10",
		LastSeenUserAgent: "Aurora Console",
	}}

	if err := service.Apply(context.Background(), updates); err != nil {
		t.Fatalf("apply presence projection: %v", err)
	}
	if len(repository.updates) != 1 || repository.updates[0] != updates[0] {
		t.Fatalf("repository received unexpected presence batch: %#v", repository.updates)
	}
}

func TestDeviceSessionCapacityEvictionServicePropagatesDurableFailure(t *testing.T) {
	repository := &deviceSessionCapacityEvictionRepositoryStub{err: errors.New("postgres unavailable")}
	service := iamService.NewDeviceSessionCapacityEvictionService(repository)
	userID := uuid.New()
	deviceID := uuid.New()

	err := service.Evict(context.Background(), userID, []uuid.UUID{deviceID})
	if !errors.Is(err, repository.err) {
		t.Fatalf("expected durable failure to stay retryable, got %v", err)
	}
	if repository.userID != userID || len(repository.deviceIDs) != 1 || repository.deviceIDs[0] != deviceID {
		t.Fatalf("repository received unexpected eviction: user=%s devices=%v", repository.userID, repository.deviceIDs)
	}
}
