package iamSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
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
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: DeviceSelfService quản lý thiết bị cho chính user cá nhân
type DeviceSelfService struct {
	deviceRepo       iamRepoInterface.DeviceSelfRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	registry         *cacheengine.CacheRegistry
	natsConn         *nats.Conn
}

// [COMMENT]: NewDeviceSelfService khởi tạo thể hiện DeviceSelfService
func NewDeviceSelfService(
	deviceRepo iamRepoInterface.DeviceSelfRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	registry *cacheengine.CacheRegistry,
	natsConn *nats.Conn,
) iamSvcInterface.DeviceSelfService {
	return &DeviceSelfService{
		deviceRepo:       deviceRepo,
		refreshTokenRepo: refreshTokenRepo,
		registry:         registry,
		natsConn:         natsConn,
	}
}

// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user
func (s *DeviceSelfService) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error) {
	var items []iamEntity.DevicePresence
	var listErr error
	var activeDevicesRes *iamproto.GetActiveDevicesResponse
	var natsErr error

	var wg sync.WaitGroup
	wg.Add(2)

	// [COMMENT]: Nhánh 1: Truy vấn PostgreSQL (I/O Bound) lấy DevicePresence thô
	go func() {
		defer wg.Done()
		items, listErr = s.deviceRepo.ListDevicesByUserID(ctx, userID, limit, offset)
	}()

	// [COMMENT]: Nhánh 2: Truy vấn danh sách session hoạt động từ ACR qua NATS Core (I/O Bound)
	go func() {
		defer wg.Done()
		req := &iamproto.GetActiveDevicesRequest{
			UserId: userID.String(),
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			natsErr = err
			return
		}
		msg, err := s.natsConn.RequestWithContext(ctx, "iam.device.get_active_sessions", reqBytes)
		if err != nil {
			natsErr = err
			return
		}
		activeDevicesRes = &iamproto.GetActiveDevicesResponse{}
		if err = proto.Unmarshal(msg.Data, activeDevicesRes); err != nil {
			natsErr = err
			activeDevicesRes = nil
			return
		}
	}()

	wg.Wait()

	if listErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	if natsErr != nil {
		// [COMMENT]: Nếu lỗi kết nối NATS, ghi nhận lỗi và tiếp tục xử lý (IsOnline mặc định false)
		iamMetrics.Downstream(ctx, "broker", "ListMyDevicesActiveQuery", iamMetrics.OutcomeFailureUnknown, 0, natsErr)
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
func (s *DeviceSelfService) RevokeMyDevice(ctx context.Context, userID uuid.UUID, clientDeviceID string, currentClientDeviceID string) error {
	serviceOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, serviceOutcome)
	}()

	if clientDeviceID == currentClientDeviceID {
		serviceOutcome = iamMetrics.OutcomePreConditionFailed
		return apperr.Wrap(iamTaxonomy.ErrActionNotAllowed, nil, "action_not_allowed")
	}

	repoStart := time.Now()
	revokeErr := s.deviceRepo.RevokeDeviceByClientDeviceIDAndUserID(ctx, clientDeviceID, userID, currentClientDeviceID)
	if revokeErr != nil {
		if errors.Is(revokeErr, iamTaxonomy.ErrZeroRowsAffected) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByClientDeviceIDAndUserID", iamMetrics.OutcomePreConditionFailed, time.Since(repoStart), revokeErr)
			serviceOutcome = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrInvalidSession, revokeErr, "invalid_session")
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByClientDeviceIDAndUserID", iamMetrics.OutcomeFailureUnknown, time.Since(repoStart), revokeErr)
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}

	// [COMMENT]: Nhánh gởi tín hiệu xóa session sang ACR chạy bất đồng bộ bằng Goroutine nền
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:          userID.String(),
			ClientDeviceIds: []string{clientDeviceID},
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			return
		}
		// Gửi yêu cầu qua NATS Request-Reply đến ACR trong background thread
		_, _ = s.natsConn.RequestWithContext(bgCtx, "iam.device.revoke_sessions", reqBytes)
	}()

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByClientDeviceIDAndUserID", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)
	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.revoked", "warning")
	return nil
}
func (s *DeviceSelfService) LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentTrackedDeviceID *uuid.UUID) (int64, error) {
	devices, listErr := s.deviceRepo.ListDevicesByUserID(ctx, userID, 100, 0)
	if listErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	var otherDeviceIDs []string
	for _, dev := range devices {
		// [COMMENT]: Chỉ thu hồi các thiết bị đang hoạt động (chưa bị revoke)
		if dev.RevokedAt == nil {
			if currentTrackedDeviceID == nil || dev.ID != currentTrackedDeviceID.String() {
				otherDeviceIDs = append(otherDeviceIDs, dev.ID)
			}
		}
	}

	if _, revokeErr := s.deviceRepo.RevokeOtherDevicesByUserID(ctx, userID, currentTrackedDeviceID); revokeErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}
	affected, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByUserID(ctx, userID, currentTrackedDeviceID)
	if revokeTokenErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeTokenErr, "dependency_error")
	}

	if len(otherDeviceIDs) > 0 {
		// [COMMENT]: Nhánh gởi tín hiệu xóa session sang ACR chạy bất đồng bộ bằng Goroutine nền
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := &iamproto.RevokeUserSessionsByDevicesRequest{
				UserId:          userID.String(),
				ClientDeviceIds: otherDeviceIDs,
			}
			reqBytes, err := proto.Marshal(req)
			if err != nil {
				return
			}
			// Gửi yêu cầu qua NATS Request-Reply đến ACR trong background thread
			_, _ = s.natsConn.RequestWithContext(bgCtx, "iam.device.revoke_sessions", reqBytes)
		}()
	}

	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.logout_others", "warning")
	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	return affected, nil
}

// [COMMENT]: LogoutAllDevices đăng xuất hoàn toàn trên toàn bộ thiết bị
func (s *DeviceSelfService) LogoutAllDevices(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.LogoutOtherDevices(ctx, userID, nil)
}

// [COMMENT]: RegisterLoginDevice đăng ký thiết bị mới đăng nhập
func (s *DeviceSelfService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

// [COMMENT]: TouchDeviceLastSeen cập nhật mốc thời gian truy cập
func (s *DeviceSelfService) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
	return s.deviceRepo.TouchDeviceLastSeen(ctx, deviceID)
}

// [COMMENT]: EvictExcessDevicesIfNeeded dọn dẹp thiết bị vượt ngưỡng
func (s *DeviceSelfService) EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID) {
	if s == nil || s.deviceRepo == nil {
		return
	}

	rdb := s.registry.L2.Client()
	userIDStr := userID.String()
	lockKey := "iam:user:device:cap_lock:" + userIDStr
	ownerToken := uuid.NewString()

	lockToken := ""
	ok, lockErr := rdb.SetNX(ctx, lockKey, ownerToken, 2*time.Second).Result()
	if lockErr != nil {
		iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeLockBusy)
	} else if !ok {
		iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeLockBusy)
		return
	} else {
		lockToken = ownerToken
		defer func() {
			lua := `
			local v = redis.call("get", KEYS[1])
			if not v then
				return 0
			end
			if v ~= ARGV[1] then
				return 0
			end
			redis.call("del", KEYS[1])
			return 1
			`
			_, _ = s.registry.Exec.Execute(context.Background(), lua, []string{lockKey}, lockToken)
		}()
	}

	const userDeviceCap = 50
	evicted, err := s.deviceRepo.EvictExcessDevices(ctx, userID, userDeviceCap)
	if err != nil || len(evicted) == 0 {
		return
	}
	deviceIDs := make([]uuid.UUID, 0, len(evicted))
	for _, item := range evicted {
		deviceIDs = append(deviceIDs, item.DeviceID)
	}
	if s.refreshTokenRepo != nil {
		_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDsAndUserID(ctx, userID, deviceIDs)
	}

	if len(deviceIDs) > 0 {
		deviceIDS := make([]string, 0, len(deviceIDs))
		for _, dID := range deviceIDs {
			deviceIDS = append(deviceIDS, dID.String())
		}
		req := &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:          userID.String(),
			ClientDeviceIds: deviceIDS,
		}
		if reqBytes, err := proto.Marshal(req); err == nil {
			// [COMMENT]: Gửi yêu cầu thu hồi session qua NATS đến ACR với context nền để tránh bị cancel giữa chừng
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = s.natsConn.RequestWithContext(bgCtx, "iam.device.revoke_sessions", reqBytes)
		}
	}

	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	extras := map[string]string{
		"reason":        "cap_exceeded",
		"evicted_count": strconv.Itoa(len(evicted)),
	}
	s.PublishDeviceAuditAsync(ctx, userID, "device.evicted_capacity", "warning", extras)
}

// [COMMENT]: ReconcileDeviceCap định kỳ dọn dẹp các thiết bị vượt ngưỡng
func (s *DeviceSelfService) ReconcileDeviceCap(ctx context.Context, batch int) (int, error) {
	if s == nil || s.deviceRepo == nil {
		return 0, nil
	}
	const userDeviceCap = 50
	users, err := s.deviceRepo.ListUsersExceedingDeviceCap(ctx, userDeviceCap, batch)
	if err != nil {
		return 0, err
	}
	for _, userID := range users {
		s.EvictExcessDevicesIfNeeded(ctx, userID)
	}
	return len(users), nil
}

// [COMMENT]: PublishDeviceAuditAsync ghi nhận sự kiện nhật ký thiết bị bất đồng bộ
func (s *DeviceSelfService) PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, extras map[string]string) {
	var ip, ua string
	if v, ok := ctx.Value(constant.RemoteIPKey).(string); ok {
		ip = v
	}
	if v, ok := ctx.Value(constant.UserAgentKey).(string); ok {
		ua = v
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		op := constant.GetOperation(ctx)
		bgCtx = constant.WithOperation(bgCtx, op)
		bgCtx = context.WithValue(bgCtx, constant.RemoteIPKey, ip)
		bgCtx = context.WithValue(bgCtx, constant.UserAgentKey, ua)
		_ = s.deviceRepo.InsertAuditEvent(bgCtx, &userID, event, severity)
		iamMetrics.ServiceCall(bgCtx, iamMetrics.OutcomeSuccess)
	}()
}

// [COMMENT]: GetActiveDeviceID trả về ID thiết bị đang hoạt động khớp với fingerprint
func (s *DeviceSelfService) GetActiveDeviceID(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
	fp := sha256.Sum256([]byte(devicePublicKey))
	fingerprint := hex.EncodeToString(fp[:])
	return s.deviceRepo.GetActiveDeviceID(ctx, userID, fingerprint)
}
