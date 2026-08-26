package unit

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
)

type deviceRuntimeRevokeRepositoryStub struct {
	oneResult     iamEntity.DeviceRuntimeRevokeResult
	otherAffected int64
	err           error
	lastOne       iamEntity.DeviceRuntimeRevokeDevice
	lastOthers    iamEntity.DeviceRuntimeRevokeOthers
	claimed       []iamEntity.DeviceRuntimeRevokeOutboxEvent
	publishedID   int64
	failedID      int64
	deadID        int64
}

func (r *deviceRuntimeRevokeRepositoryStub) RevokeDevice(_ context.Context, command iamEntity.DeviceRuntimeRevokeDevice) (iamEntity.DeviceRuntimeRevokeResult, error) {
	r.lastOne = command
	return r.oneResult, r.err
}

func (r *deviceRuntimeRevokeRepositoryStub) RevokeOtherDevices(_ context.Context, command iamEntity.DeviceRuntimeRevokeOthers) (int64, error) {
	r.lastOthers = command
	return r.otherAffected, r.err
}

func (r *deviceRuntimeRevokeRepositoryStub) Claim(context.Context, int) ([]iamEntity.DeviceRuntimeRevokeOutboxEvent, error) {
	return r.claimed, r.err
}

func (r *deviceRuntimeRevokeRepositoryStub) MarkPublished(_ context.Context, id int64) error {
	r.publishedID = id
	return r.err
}

func (r *deviceRuntimeRevokeRepositoryStub) MarkFailed(_ context.Context, id int64, _ string) error {
	r.failedID = id
	return r.err
}

func (r *deviceRuntimeRevokeRepositoryStub) MarkDead(_ context.Context, id int64, _ string) error {
	r.deadID = id
	return r.err
}

func TestDeviceRuntimeRevokeServicePersistsSingleDeviceIntent(t *testing.T) {
	repo := &deviceRuntimeRevokeRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{TargetExists: true}}
	service := iamService.NewDeviceRuntimeRevokeService(repo)
	userID := uuid.New()
	targetDeviceID := uuid.New()
	currentDeviceID := uuid.New()

	if err := service.RevokeDevice(context.Background(), userID, targetDeviceID, currentDeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if repo.lastOne.EventID == uuid.Nil || repo.lastOne.UserID != userID || repo.lastOne.ClientDeviceID != targetDeviceID || repo.lastOne.CurrentDeviceID != currentDeviceID {
		t.Fatalf("unexpected durable revoke command: %#v", repo.lastOne)
	}
}

func TestDeviceRuntimeRevokeServiceRejectsCurrentOrMissingDevice(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()

	currentRepo := &deviceRuntimeRevokeRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{TargetExists: true, CurrentDevice: true}}
	if err := iamService.NewDeviceRuntimeRevokeService(currentRepo).RevokeDevice(context.Background(), userID, deviceID, deviceID); !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("expected current-device error, got %v", err)
	}

	missingRepo := &deviceRuntimeRevokeRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{}}
	if err := iamService.NewDeviceRuntimeRevokeService(missingRepo).RevokeDevice(context.Background(), userID, deviceID, uuid.New()); !errors.Is(err, iamTaxonomy.ErrInvalidSession) {
		t.Fatalf("expected missing-device error, got %v", err)
	}
}

func TestDeviceRuntimeRevokeServiceKeepsOutboxSettlementInWorkflow(t *testing.T) {
	repo := &deviceRuntimeRevokeRepositoryStub{otherAffected: 3}
	service := iamService.NewDeviceRuntimeRevokeService(repo)
	userID := uuid.New()
	currentDeviceID := uuid.New()

	affected, err := service.RevokeOtherDevices(context.Background(), userID, currentDeviceID)
	if err != nil || affected != 3 {
		t.Fatalf("revoke other devices: affected=%d err=%v", affected, err)
	}
	if repo.lastOthers.EventID == uuid.Nil || repo.lastOthers.UserID != userID || repo.lastOthers.CurrentDeviceID != currentDeviceID {
		t.Fatalf("unexpected durable other-device command: %#v", repo.lastOthers)
	}
	if err := service.MarkPublished(context.Background(), 41); err != nil || repo.publishedID != 41 {
		t.Fatalf("mark published: err=%v id=%d", err, repo.publishedID)
	}
}
