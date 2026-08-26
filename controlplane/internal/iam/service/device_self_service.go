package iamSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: DeviceSelfService quản lý thiết bị cho chính user cá nhân
type DeviceSelfService struct {
	deviceRepo  iamRepoInterface.DeviceSelfRepository
	registry    *cacheengine.CacheRegistry
	sharedRedis *goredis.Client
	metrics     observability.WorkflowRecorder
}

// [COMMENT]: NewDeviceSelfService khởi tạo thể hiện DeviceSelfService
func NewDeviceSelfService(
	deviceRepo iamRepoInterface.DeviceSelfRepository,
	registry *cacheengine.CacheRegistry,
	sharedRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.DeviceSelfService {
	return &DeviceSelfService{
		deviceRepo:  deviceRepo,
		registry:    registry,
		sharedRedis: sharedRedis,
		metrics:     metrics,
	}
}

// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user
func (s *DeviceSelfService) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

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
		requestID := uuid.New()
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
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	if runtimeErr != nil {
		// [COMMENT]: Runtime visibility là soft-state; DB list vẫn trả được và IsOnline mặc định false.
		_ = runtimeErr
	}

	// Map active devices to O(1) map
	activeMap := make(map[string]int64)
	if activeDevicesRes != nil {
		for _, dev := range activeDevicesRes.ActiveDevices {
			activeMap[strings.TrimSpace(dev.ClientDeviceId)] = dev.LastSeenAt
		}
	}

	// [COMMENT]: Cập nhật trạng thái online và thời gian hoạt động cuối cùng của từng thiết bị trong mảng items
	for i := range items {
		if lastSeen, ok := activeMap[strings.TrimSpace(items[i].ID)]; ok {
			items[i].IsOnline = true
			if lastSeen > 0 {
				ts := time.Unix(lastSeen, 0).UTC()
				items[i].LastSeenAt = &ts
			}
		}
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.DeviceListResult{Devices: items, Total: int64(len(items))}, nil
}

// [COMMENT]: RegisterLoginDevice đăng ký thiết bị mới đăng nhập
func (s *DeviceSelfService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

// [COMMENT]: ResolveDeviceIDByKey trả về ID thiết bị khớp với fingerprint của khóa công khai
func (s *DeviceSelfService) ResolveDeviceIDByKey(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
	fp := sha256.Sum256([]byte(devicePublicKey))
	fingerprint := hex.EncodeToString(fp[:])
	return s.deviceRepo.ResolveDeviceIDByFingerprint(ctx, userID, fingerprint)
}
