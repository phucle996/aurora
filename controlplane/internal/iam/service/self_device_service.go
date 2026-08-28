package iamSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// SelfDeviceService implements device operations scoped to the verified `/me` user.
type SelfDeviceService struct {
	deviceRepo  iamRepoInterface.SelfDeviceRepository
	registry    *cacheengine.CacheRegistry
	sharedRedis *goredis.Client
	authRedis   *goredis.Client
	metrics     observability.WorkflowRecorder
}

func NewSelfDeviceService(
	deviceRepo iamRepoInterface.SelfDeviceRepository,
	registry *cacheengine.CacheRegistry,
	sharedRedis *goredis.Client,
	authRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.SelfDeviceService {
	return &SelfDeviceService{
		deviceRepo:  deviceRepo,
		registry:    registry,
		sharedRedis: sharedRedis,
		authRedis:   authRedis,
		metrics:     metrics,
	}
}

// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user
func (s *SelfDeviceService) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (output *iamEntity.DeviceListResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	var items []iamEntity.DevicePresence
	var listErr error
	var activeDevicesRes *iamproto.GetActiveDevicesResponse
	var runtimeErr error

	var wg sync.WaitGroup
	wg.Add(2)

	// [COMMENT]: Nhánh 1: Truy vấn PostgreSQL (I/O Bound) lấy DevicePresence thô
	go func() {
		defer wg.Done()
		items, listErr = s.deviceRepo.ListDevicesByUserID(ctx, userID, limit, offset)
	}()

	// [COMMENT]: Nhánh 2: realtime query tới ACR qua Shared Redis PubSub; dữ liệu
	// session vẫn nằm trong Auth Redis và không bị copy thành business record.
	go func() {
		defer wg.Done()
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req := &iamproto.GetActiveDevicesRequest{
			UserId: userID.String(),
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			runtimeErr = err
			return
		}
		requestID, err := uuid.NewV7()
		if err != nil {
			runtimeErr = err
			return
		}
		replyChannel := "iam.device.get_active_sessions.reply." + requestID.String()
		pubsub := s.sharedRedis.Subscribe(queryCtx, replyChannel)
		defer pubsub.Close()
		if _, err = pubsub.Receive(queryCtx); err != nil {
			runtimeErr = err
			return
		}
		envelope := make([]byte, 0, 16+len(reqBytes))
		envelope = append(envelope, requestID[:]...)
		envelope = append(envelope, reqBytes...)
		subscribers, err := s.sharedRedis.Publish(queryCtx, "iam.device.get_active_sessions", envelope).Result()
		if err != nil {
			runtimeErr = err
			return
		}
		if subscribers == 0 {
			runtimeErr = errors.New("no ACR replica subscribed to active-session query")
			return
		}
		var message *goredis.Message
		select {
		case <-queryCtx.Done():
			runtimeErr = queryCtx.Err()
			return
		case message = <-pubsub.Channel(goredis.WithChannelSize(1)):
		}
		if message == nil {
			runtimeErr = errors.New("active-session reply channel closed")
			return
		}
		activeDevicesRes = &iamproto.GetActiveDevicesResponse{}
		if err = proto.Unmarshal([]byte(message.Payload), activeDevicesRes); err != nil {
			runtimeErr = err
			activeDevicesRes = nil
			return
		}
	}()

	wg.Wait()

	if listErr != nil {
		return nil, listErr
	}

	if runtimeErr != nil {
		// [COMMENT]: Runtime visibility là soft-state; DB list vẫn trả được và IsOnline mặc định false.
		_ = runtimeErr
	}

	// Map active devices to O(1) map
	activeMap := make(map[uuid.UUID]int64)
	if activeDevicesRes != nil {
		for _, dev := range activeDevicesRes.ActiveDevices {
			if parsed, err := uuid.Parse(strings.TrimSpace(dev.ClientDeviceId)); err == nil {
				activeMap[parsed] = dev.LastSeenAt
			}
		}
	}

	// [COMMENT]: Cập nhật trạng thái online và thời gian hoạt động cuối cùng của từng thiết bị trong mảng items
	for i := range items {
		if lastSeen, ok := activeMap[items[i].ID]; ok {
			items[i].IsOnline = true
			if lastSeen > 0 {
				ts := time.Unix(lastSeen, 0).UTC()
				items[i].LastSeenAt = &ts
			}
		}
	}
	return &iamEntity.DeviceListResult{Devices: items, Total: int64(len(items))}, nil
}

// [COMMENT]: RegisterLoginDevice đăng ký thiết bị mới đăng nhập hoặc cập nhật thiết bị hiện có (Atomic CTE Upsert)
func (s *SelfDeviceService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	if device.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("iam device service: generate device uuid v7: %w", err)
		}
		device.ID = id
	}
	if device.PublicKeyFingerprint == "" && device.PublicKey != "" {
		fp := sha256.Sum256([]byte(device.PublicKey))
		device.PublicKeyFingerprint = hex.EncodeToString(fp[:])
	}
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

func (s *SelfDeviceService) RevokeMyDevice(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceID uuid.UUID,
	currentDeviceID uuid.UUID,
) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed), errors.Is(err, iamTaxonomy.ErrInvalidSession):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	revokeResult, err := s.deviceRepo.RevokeSelfDevice(ctx, iamEntity.DeviceRuntimeRevokeDevice{
		UserID:          userID,
		ClientDeviceID:  clientDeviceID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		return err
	}
	if revokeResult.CurrentDevice {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if !revokeResult.TargetExists {
		return iamTaxonomy.ErrInvalidSession
	}

	// [COMMENT]: Thu hồi trực tiếp các session thuộc clientDeviceID trên Auth Redis
	deviceIndexKey := "iam:device_access_index:{" + clientDeviceID.String() + "}"
	userIndexKey := "iam:user_access_index:{" + userID.String() + "}"
	if err := s.authRedis.Eval(ctx, `
		local dev_key = KEYS[1]
		local user_key = KEYS[2]
		local sessions = redis.call('SMEMBERS', dev_key)
		for _, session_key in ipairs(sessions) do
			redis.call('DEL', session_key)
			redis.call('SREM', user_key, session_key)
			local last_colon = string.find(session_key, ":[^:]*$")
			if last_colon then
				local access_key = string.sub(session_key, last_colon + 1)
				redis.call('DEL', 'iam:session_alias:' .. access_key)
			end
		end
		redis.call('DEL', dev_key)
		return #sessions
	`, []string{deviceIndexKey, userIndexKey}).Err(); err != nil {
		return err
	}

	return nil
}

func (s *SelfDeviceService) LogoutOtherDevices(
	ctx context.Context,
	userID uuid.UUID,
	currentDeviceID uuid.UUID,
) (affected int64, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrInvalidSession):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	revokeResult, err := s.deviceRepo.RevokeOtherSelfDevices(ctx, iamEntity.DeviceRuntimeRevokeOthers{
		UserID:          userID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		return 0, err
	}

	// [COMMENT]: Thu hồi trực tiếp các session của các thiết bị bị thu hồi trên Auth Redis
	userIndexKey := "iam:user_access_index:{" + userID.String() + "}"
	for _, devID := range revokeResult.RevokedDeviceIDs {
		deviceIndexKey := "iam:device_access_index:{" + devID.String() + "}"
		if err := s.authRedis.Eval(ctx, `
			local dev_key = KEYS[1]
			local user_key = KEYS[2]
			local sessions = redis.call('SMEMBERS', dev_key)
			for _, session_key in ipairs(sessions) do
				redis.call('DEL', session_key)
				redis.call('SREM', user_key, session_key)
				local last_colon = string.find(session_key, ":[^:]*$")
				if last_colon then
					local access_key = string.sub(session_key, last_colon + 1)
					redis.call('DEL', 'iam:session_alias:' .. access_key)
				end
			end
			redis.call('DEL', dev_key)
			return #sessions
		`, []string{deviceIndexKey, userIndexKey}).Err(); err != nil {
			return 0, err
		}
	}

	return revokeResult.Affected, nil
}

func (s *SelfDeviceService) ApplyDevicePresenceProjection(
	ctx context.Context,
	updates []iamEntity.DevicePresenceUpdate,
) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.deviceRepo.ApplyDevicePresenceProjection(ctx, updates)
}

func (s *SelfDeviceService) ApplyDeviceSessionCapacityEviction(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceIDs []uuid.UUID,
) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.deviceRepo.ApplyDeviceSessionCapacityEviction(ctx, userID, clientDeviceIDs)
}
