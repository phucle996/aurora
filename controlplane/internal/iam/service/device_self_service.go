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

	// [COMMENT]: Nhánh gởi tín hiệu xóa session sang ACR chạy bất đồng bộ bằng Goroutine nền
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:          userID.String(),
			ClientDeviceIds: []string{clientDeviceID.String()},
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			return
		}
		// Gửi yêu cầu qua NATS Request-Reply đến ACR trong background thread
		_, _ = s.natsConn.RequestWithContext(bgCtx, "iam.device.revoke_sessions", reqBytes)
	}()

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeMyDevice", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)
	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.revoked", "warning")
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
		// [COMMENT]: Nhánh gởi tín hiệu xóa session sang ACR chạy bất đồng bộ bằng Goroutine nền
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

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
				return
			}
			// Gửi yêu cầu qua NATS Request-Reply đến ACR trong background thread
			_, _ = s.natsConn.RequestWithContext(bgCtx, "iam.device.revoke_sessions", reqBytes)
		}()
	}

	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.logout_others", "warning")
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

// [COMMENT]: RevokeDevicesByClientDeviceIDs thu hồi hàng loạt thiết bị của một user dựa trên danh sách client_device_id,
// đồng thời xóa bỏ Refresh Token tương ứng trong database. Được gọi khi nhận sự kiện Evicted từ ACR qua NATS.
func (s *DeviceSelfService) RevokeDevicesByClientDeviceIDs(ctx context.Context, userID uuid.UUID, clientDeviceIDs []string) error {
	if s == nil || s.deviceRepo == nil {
		return nil
	}
	if len(clientDeviceIDs) == 0 {
		return nil
	}

	// 1. Cập nhật trạng thái 'revoked' cho các thiết bị trong DB
	if err := s.deviceRepo.RevokeDevicesByClientDeviceIDs(ctx, userID, clientDeviceIDs); err != nil {
		return fmt.Errorf("iam service: revoke devices by client device ids: %w", err)
	}

	// 2. Thu hồi các refresh token tương ứng
	if s.refreshTokenRepo != nil {
		if _, err := s.refreshTokenRepo.DeleteTokensByClientDeviceIDs(ctx, userID, clientDeviceIDs); err != nil {
			return fmt.Errorf("iam service: delete tokens by client device ids: %w", err)
		}
	}

	return nil
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
