package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type deviceWorkflowRecorderSpy struct {
	callCount int
	result    observability.Result
	reason    observability.Reason
}

func (r *deviceWorkflowRecorderSpy) ObserveWorkflow(_ context.Context, result observability.Result, reason observability.Reason, _ time.Duration) {
	r.callCount++
	r.result = result
	r.reason = reason
}

type selfDeviceRepositoryStub struct {
	oneResult    iamEntity.DeviceRuntimeRevokeResult
	otherResult  iamEntity.DeviceRuntimeRevokeOthersResult
	err          error
	lastOne      iamEntity.DeviceRuntimeRevokeDevice
	lastOthers   iamEntity.DeviceRuntimeRevokeOthers
	updates      []iamEntity.DevicePresenceUpdate
	evictionUser uuid.UUID
	evictionIDs  []uuid.UUID
}

func (r *selfDeviceRepositoryStub) UpsertLoginDevice(_ context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return &device, r.err
}

func (r *selfDeviceRepositoryStub) ListDevicesByUserID(context.Context, uuid.UUID, int, int) ([]iamEntity.DevicePresence, error) {
	return nil, r.err
}

func (r *selfDeviceRepositoryStub) RevokeSelfDevice(_ context.Context, command iamEntity.DeviceRuntimeRevokeDevice) (iamEntity.DeviceRuntimeRevokeResult, error) {
	r.lastOne = command
	return r.oneResult, r.err
}

func (r *selfDeviceRepositoryStub) RevokeOtherSelfDevices(_ context.Context, command iamEntity.DeviceRuntimeRevokeOthers) (iamEntity.DeviceRuntimeRevokeOthersResult, error) {
	r.lastOthers = command
	return r.otherResult, r.err
}

func (r *selfDeviceRepositoryStub) ApplyDevicePresenceProjection(_ context.Context, updates []iamEntity.DevicePresenceUpdate) error {
	r.updates = updates
	return r.err
}

func (r *selfDeviceRepositoryStub) ApplyDeviceSessionCapacityEviction(_ context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) error {
	r.evictionUser = userID
	r.evictionIDs = deviceIDs
	return r.err
}

func TestSelfDeviceServicePersistsSingleDeviceRevokeIntent(t *testing.T) {
	redisServer := miniredis.RunT(t)
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer authRedis.Close()

	repo := &selfDeviceRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{TargetExists: true}}
	service := iamService.NewSelfDeviceService(repo, nil, nil, authRedis, observability.NewNoopWorkflowRecorder())
	userID := uuid.New()
	targetDeviceID := uuid.New()
	currentDeviceID := uuid.New()

	if err := service.RevokeMyDevice(context.Background(), userID, targetDeviceID, currentDeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if repo.lastOne.UserID != userID || repo.lastOne.ClientDeviceID != targetDeviceID || repo.lastOne.CurrentDeviceID != currentDeviceID {
		t.Fatalf("unexpected durable revoke command: %#v", repo.lastOne)
	}
}

func TestSelfDeviceServiceRejectsCurrentOrMissingDevice(t *testing.T) {
	redisServer := miniredis.RunT(t)
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer authRedis.Close()

	userID := uuid.New()
	deviceID := uuid.New()

	currentRepo := &selfDeviceRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{TargetExists: true, CurrentDevice: true}}
	metrics := &deviceWorkflowRecorderSpy{}
	if err := iamService.NewSelfDeviceService(currentRepo, nil, nil, authRedis, metrics).RevokeMyDevice(context.Background(), userID, deviceID, deviceID); !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("expected current-device error, got %v", err)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultRejected || metrics.reason != observability.ReasonForbidden {
		t.Fatalf("unexpected current-device workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}

	missingRepo := &selfDeviceRepositoryStub{oneResult: iamEntity.DeviceRuntimeRevokeResult{}}
	if err := iamService.NewSelfDeviceService(missingRepo, nil, nil, authRedis, observability.NewNoopWorkflowRecorder()).RevokeMyDevice(context.Background(), userID, deviceID, uuid.New()); !errors.Is(err, iamTaxonomy.ErrInvalidSession) {
		t.Fatalf("expected missing-device error, got %v", err)
	}
}

func TestSelfDeviceServiceReturnsRawRevokeRepositoryFailure(t *testing.T) {
	redisServer := miniredis.RunT(t)
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer authRedis.Close()

	repositoryErr := errors.New("postgres unavailable")
	repo := &selfDeviceRepositoryStub{err: repositoryErr}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewSelfDeviceService(repo, nil, nil, authRedis, metrics)

	err := service.RevokeMyDevice(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if err != repositoryErr {
		t.Fatalf("expected raw repository error identity, got %v", err)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultFailure || metrics.reason != observability.ReasonInternal {
		t.Fatalf("unexpected failed workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}

func TestSelfDeviceServiceReturnsRuntimeEvictionFailure(t *testing.T) {
	redisServer := miniredis.RunT(t)
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	redisServer.Close()
	defer authRedis.Close()

	repo := &selfDeviceRepositoryStub{
		oneResult: iamEntity.DeviceRuntimeRevokeResult{TargetExists: true},
		otherResult: iamEntity.DeviceRuntimeRevokeOthersResult{
			Affected:         1,
			RevokedDeviceIDs: []uuid.UUID{uuid.New()},
		},
	}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewSelfDeviceService(repo, nil, nil, authRedis, metrics)

	if err := service.RevokeMyDevice(context.Background(), uuid.New(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected single-device runtime eviction error")
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultFailure || metrics.reason != observability.ReasonInternal {
		t.Fatalf("unexpected runtime-failure observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}

	metrics.callCount = 0
	if affected, err := service.LogoutOtherDevices(context.Background(), uuid.New(), uuid.New()); err == nil || affected != 0 {
		t.Fatalf("expected logout-others runtime eviction error, affected=%d err=%v", affected, err)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultFailure || metrics.reason != observability.ReasonInternal {
		t.Fatalf("unexpected logout-others failure observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}

func TestSelfDeviceServiceKeepsRuntimeRevokeSettlementInWorkflow(t *testing.T) {
	redisServer := miniredis.RunT(t)
	authRedis := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer authRedis.Close()

	repo := &selfDeviceRepositoryStub{otherResult: iamEntity.DeviceRuntimeRevokeOthersResult{Affected: 3}}
	metrics := &deviceWorkflowRecorderSpy{}
	service := iamService.NewSelfDeviceService(repo, nil, nil, authRedis, metrics)
	userID := uuid.New()
	currentDeviceID := uuid.New()

	affected, err := service.LogoutOtherDevices(context.Background(), userID, currentDeviceID)
	if err != nil || affected != 3 {
		t.Fatalf("revoke other devices: affected=%d err=%v", affected, err)
	}
	if repo.lastOthers.UserID != userID || repo.lastOthers.CurrentDeviceID != currentDeviceID {
		t.Fatalf("unexpected durable other-device command: %#v", repo.lastOthers)
	}
	if metrics.callCount != 1 || metrics.result != observability.ResultSuccess || metrics.reason != observability.ReasonNone {
		t.Fatalf("unexpected logout-others workflow observation: calls=%d result=%s reason=%s", metrics.callCount, metrics.result, metrics.reason)
	}
}
