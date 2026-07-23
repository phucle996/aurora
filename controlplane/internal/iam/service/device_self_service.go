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
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: DeviceSelfService quản lý thiết bị cho chính user cá nhân
type DeviceSelfService struct {
	deviceRepo       iamRepoInterface.DeviceSelfRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	registry         *cacheengine.CacheRegistry
	sharedRedis      *goredis.Client
}

// [COMMENT]: NewDeviceSelfService khởi tạo thể hiện DeviceSelfService
func NewDeviceSelfService(
	deviceRepo iamRepoInterface.DeviceSelfRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	registry *cacheengine.CacheRegistry,
	sharedRedis *goredis.Client,
) iamSvcInterface.DeviceSelfService {
	return &DeviceSelfService{
		deviceRepo:       deviceRepo,
		refreshTokenRepo: refreshTokenRepo,
		registry:         registry,
		sharedRedis:      sharedRedis,
	}
}

// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user
func (s *DeviceSelfService) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error) {
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
		iamMetrics.Downstream(ctx, "broker", "ListMyDevicesActiveQuery", iamMetrics.OutcomeFailureUnknown, 0, runtimeErr)
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
	return &iamEntity.DeviceListResult{Devices: items, Total: int64(len(items))}, nil
}

// [COMMENT]: RevokeMyDevice thu hồi thiết bị chỉ định theo client_device_id
func (s *DeviceSelfService) RevokeMyDevice(ctx context.Context, userID uuid.UUID, clientDeviceID uuid.UUID, currentDeviceID uuid.UUID) error {
	serviceOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, serviceOutcome)
	}()

	repoStart := time.Now()
	revokeErr := s.deviceRepo.RevokeMyDevice(ctx, clientDeviceID, userID, currentDeviceID)
	if revokeErr != nil {
		if errors.Is(revokeErr, iamTaxonomy.ErrActionNotAllowed) {
			serviceOutcome = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrActionNotAllowed, nil, "action_not_allowed")
		}
		if errors.Is(revokeErr, iamTaxonomy.ErrZeroRowsAffected) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyDevice", iamMetrics.OutcomePreConditionFailed, time.Since(repoStart), revokeErr)
			serviceOutcome = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrInvalidSession, revokeErr, "invalid_session")
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyDevice", iamMetrics.OutcomeFailureUnknown, time.Since(repoStart), revokeErr)
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}

	req := &iamproto.RevokeUserSessionsByDevicesRequest{
		UserId:          userID.String(),
		ClientDeviceIds: []string{clientDeviceID.String()},
	}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	// [COMMENT]: Revoke là security command nên ghi Redis Stream durable. Repository là
	// idempotent, do đó client retry sau lỗi XADD sẽ enqueue lại mà không làm hỏng DB state.
	if err := s.sharedRedis.XAdd(ctx, &goredis.XAddArgs{
		Stream: "iam:device:revoke-requests",
		Values: map[string]any{"payload": reqBytes},
	}).Err(); err != nil {
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyDevice", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)
	return nil
}

// [COMMENT]: LogoutOtherDevices đăng xuất khỏi tất cả các thiết bị khác
func (s *DeviceSelfService) LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) (int64, error) {
	repoStart := time.Now()
	otherDeviceIDs, revokeErr := s.deviceRepo.RevokeMyOtherDevices(ctx, userID, &currentDeviceID)
	if revokeErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyOtherDevices", iamMetrics.OutcomeFailureUnknown, time.Since(repoStart), revokeErr)
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyOtherDevices", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)

	if len(otherDeviceIDs) > 0 {
		clientDeviceIDs := make([]string, len(otherDeviceIDs))
		for i, id := range otherDeviceIDs {
			clientDeviceIDs[i] = id.String()
		}
		req := &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:          userID.String(),
			ClientDeviceIds: clientDeviceIDs,
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
		}
		if err := s.sharedRedis.XAdd(ctx, &goredis.XAddArgs{
			Stream: "iam:device:revoke-requests",
			Values: map[string]any{"payload": reqBytes},
		}).Err(); err != nil {
			return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
		}
	}

	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	return int64(len(otherDeviceIDs)), nil
}

// [COMMENT]: RegisterLoginDevice đăng ký thiết bị mới đăng nhập
func (s *DeviceSelfService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

// [COMMENT]: BulkTouchDevices cập nhật trạng thái hoạt động hàng loạt
func (s *DeviceSelfService) BulkTouchDevices(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error {
	return s.deviceRepo.BulkTouchDevices(ctx, updates)
}

// [COMMENT]: EvictDevices thu hồi hàng loạt thiết bị của một user dựa trên danh sách client_device_id,
// đồng thời xóa bỏ Refresh Token tương ứng trong database. Được gọi khi nhận eviction từ ACR qua Shared Redis.
func (s *DeviceSelfService) EvictDevices(ctx context.Context, userID uuid.UUID, clientDeviceIDs []string) error {

	if len(clientDeviceIDs) == 0 {
		return nil
	}
	return s.deviceRepo.EvictDevices(ctx, userID, clientDeviceIDs)
}

// [COMMENT]: ResolveDeviceIDByKey trả về ID thiết bị khớp với fingerprint của khóa công khai
func (s *DeviceSelfService) ResolveDeviceIDByKey(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
	fp := sha256.Sum256([]byte(devicePublicKey))
	fingerprint := hex.EncodeToString(fp[:])
	return s.deviceRepo.ResolveDeviceIDByFingerprint(ctx, userID, fingerprint)
}
