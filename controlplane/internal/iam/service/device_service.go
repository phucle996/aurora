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

	"unsafe"

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
	"google.golang.org/protobuf/proto"
)

type DeviceService struct {
	deviceRepo           iamRepoInterface.DeviceRepository
	refreshTokenRepo     iamRepoInterface.RefreshTokenRepository
	registry             *cacheengine.CacheRegistry
	sessionServiceClient iamproto.SessionServiceClient
}

func NewDeviceService(deviceRepo iamRepoInterface.DeviceRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	registry *cacheengine.CacheRegistry,
	sessionServiceClient iamproto.SessionServiceClient,
) iamSvcInterface.DeviceService {
	return &DeviceService{
		deviceRepo:           deviceRepo,
		refreshTokenRepo:     refreshTokenRepo,
		registry:             registry,
		sessionServiceClient: sessionServiceClient,
	}
}

// [COMMENT]: Đã xóa bỏ getUserIDFromContext vì userID hiện tại được truyền trực tiếp từ handler/transport layer qua HTTP header.

// ListMyDevices lấy danh sách các thiết bị của user.
// [COMMENT]: Nhận userID trực tiếp từ handler thay vì trích xuất từ context.
func (s *DeviceService) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error) {

	var items []iamEntity.Device
	var listErr error
	presenceByTracked := make(map[string]iamEntity.UserAccessSession, 10) // Mặc định 10 để tối ưu RAM

	var wg sync.WaitGroup
	wg.Add(2)

	// [COMMENT]: Nhánh 1: Truy vấn PostgreSQL (I/O Bound) chạy trong goroutine riêng biệt.
	go func() {
		defer wg.Done()
		items, listErr = s.deviceRepo.ListDevicesByUserID(ctx, userID, limit, offset)
	}()

	// [COMMENT]: Nhánh 2: Quét L2 Redis Cache (I/O Bound) chạy song song với DB query.
	go func() {
		defer wg.Done()
		rdb := s.registry.L2.Client()
		userIDStr := userID.String()
		indexKey := "iam:user_access_index:" + userIDStr

		// [COMMENT]: Duyệt lấy tối đa 200 sessions trong Redis.
		keys := make([]string, 0, 10)
		var cursor uint64
		for len(keys) < 200 {
			scanned, nextCursor, scanErr := rdb.SScan(ctx, indexKey, cursor, "*", 200).Result()
			if scanErr != nil {
				break
			}
			for _, accessKey := range scanned {
				keys = append(keys, "iam:user_access_session:"+userIDStr+":"+accessKey)
				if len(keys) >= 200 {
					break
				}
			}
			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}

		if len(keys) > 0 {
			if values, err := rdb.MGet(ctx, keys...).Result(); err == nil {
				for _, raw := range values {
					if raw == nil {
						continue
					}
					if rawStr, ok := raw.(string); ok {
						// [COMMENT]: Sử dụng zero-copy conversion từ string sang []byte để triệt tiêu allocations.
						var pb iamproto.UserAccessSession
						if proto.Unmarshal(unsafeStringToBytes(rawStr), &pb) == nil {
							key := strings.TrimSpace(pb.Tdid)
							if key != "" {
								presenceByTracked[key] = iamEntity.UserAccessSession{
									TrackedDeviceID: pb.Tdid,
									LastSeenAt:      pb.Lsa,
								}
							}
						}
					}
				}
			}
		}
	}()

	// [COMMENT]: Chờ cả 2 tác vụ I/O hoàn thành song song.
	wg.Wait()

	if listErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	out := make([]iamEntity.DevicePresence, 0, len(items))
	for _, device := range items {
		p := iamEntity.DevicePresence{Device: device, IsOnline: false}
		if rt, ok := presenceByTracked[strings.TrimSpace(device.ID)]; ok {
			p.IsOnline = true
			if rt.LastSeenAt > 0 {
				ts := time.Unix(rt.LastSeenAt, 0).UTC()
				p.LastSeenAt = &ts
			}
		}
		out = append(out, p)
	}
	return &iamEntity.DeviceListResult{Devices: out, Total: int64(len(out))}, nil
}

// RevokeMyDevice thu hồi quyền truy cập của một thiết bị cụ thể.
// [COMMENT]: Nhận userID trực tiếp từ handler thay vì trích xuất từ context.
func (s *DeviceService) RevokeMyDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, currentDeviceID uuid.UUID) error {
	serviceOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, serviceOutcome)
	}()

	// [COMMENT]: Chặn nhanh nếu client cố tình gửi yêu cầu tự thu hồi thiết bị hiện tại của mình.
	if deviceID == currentDeviceID {
		serviceOutcome = iamMetrics.OutcomePreConditionFailed
		return apperr.Wrap(iamTaxonomy.ErrActionNotAllowed, nil, "action_not_allowed")
	}

	// [COMMENT]: Đo lường thời gian thực thi câu lệnh SQL CTE (downstream).
	repoStart := time.Now()
	revokeErr := s.deviceRepo.RevokeDeviceByIDAndUserID(ctx, deviceID, userID, currentDeviceID)
	if revokeErr != nil {
		if errors.Is(revokeErr, iamTaxonomy.ErrZeroRowsAffected) {
			// [COMMENT]: Coi là lỗi nghiệp vụ (không hợp lệ) nhưng không phải lỗi hệ thống/downstream fail hoàn toàn.
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByIDAndUserID", iamMetrics.OutcomePreConditionFailed, time.Since(repoStart), revokeErr)
			serviceOutcome = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrInvalidSession, revokeErr, "invalid_session")
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByIDAndUserID", iamMetrics.OutcomeFailureUnknown, time.Since(repoStart), revokeErr)
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}

	// [COMMENT]: Gọi gRPC sang ACR Service để thu hồi phiên của thiết bị này trên Redis L2 (Grace Period 5s)
	if s.sessionServiceClient != nil {
		_, grpcErr := s.sessionServiceClient.RevokeUserSessionsByDevices(ctx, &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:    userID.String(),
			DeviceIds: []string{deviceID.String()},
		})
		if grpcErr != nil {
			serviceOutcome = iamMetrics.OutcomeFailureUnknown
			return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, grpcErr, "session_revocation_rpc_failed")
		}
	}

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByIDAndUserID", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)
	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.revoked", "warning")
	return nil
}

// LogoutOtherDevices đăng xuất khỏi toàn bộ thiết bị khác.
// [COMMENT]: Nhận userID trực tiếp từ handler thay vì trích xuất từ context.
func (s *DeviceService) LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentTrackedDeviceID *uuid.UUID) (int64, error) {
	// [COMMENT]: Lấy danh sách thiết bị trước khi thu hồi để thu thập các device ID cần gửi qua gRPC
	devices, listErr := s.deviceRepo.ListDevicesByUserID(ctx, userID, 100, 0)
	if listErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	var otherDeviceIDs []string
	for _, dev := range devices {
		if dev.Status != iamEntity.DeviceStatusRevoked {
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

	// [COMMENT]: Gọi gRPC sang ACR Service để thu hồi các session của các thiết bị khác trên Redis L2
	if len(otherDeviceIDs) > 0 && s.sessionServiceClient != nil {
		_, grpcErr := s.sessionServiceClient.RevokeUserSessionsByDevices(ctx, &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:    userID.String(),
			DeviceIds: otherDeviceIDs,
		})
		if grpcErr != nil {
			return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, grpcErr, "session_revocation_rpc_failed")
		}
	}

	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.logout_others", "warning")
	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	return affected, nil
}

// LogoutAllDevices đăng xuất hoàn toàn trên toàn bộ thiết bị.
// [COMMENT]: Nhận userID trực tiếp từ handler thay vì trích xuất từ context.
func (s *DeviceService) LogoutAllDevices(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.LogoutOtherDevices(ctx, userID, nil)
}

func (s *DeviceService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

func (s *DeviceService) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
	return s.deviceRepo.TouchDeviceLastSeen(ctx, deviceID)
}

func (s *DeviceService) EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID) {
	// Best-effort maintenance path: mọi lỗi ở đây không được làm fail login.
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

	// [COMMENT]: Gọi gRPC sang ACR Service để thu hồi phiên của các thiết bị vượt quá dung lượng trên Redis L2 (best-effort)
	if len(deviceIDs) > 0 && s.sessionServiceClient != nil {
		deviceIDS := make([]string, 0, len(deviceIDs))
		for _, dID := range deviceIDs {
			deviceIDS = append(deviceIDS, dID.String())
		}
		_, _ = s.sessionServiceClient.RevokeUserSessionsByDevices(ctx, &iamproto.RevokeUserSessionsByDevicesRequest{
			UserId:    userID.String(),
			DeviceIds: deviceIDS,
		})
	}

	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	extras := map[string]string{
		"reason":        "cap_exceeded",
		"evicted_count": strconv.Itoa(len(evicted)),
	}
	s.PublishDeviceAuditAsync(ctx, userID, "device.evicted_capacity", "warning", extras)
}

func (s *DeviceService) ReconcileDeviceCap(ctx context.Context, batch int) (int, error) {
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

func (s *DeviceService) PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, extras map[string]string) {
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

func (s *DeviceService) GetActiveDeviceID(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
	// [COMMENT]: Tính toán vân tay (fingerprint) của khóa công khai thiết bị để truy vấn nhanh.
	fp := sha256.Sum256([]byte(devicePublicKey))
	fingerprint := hex.EncodeToString(fp[:])

	// [COMMENT]: Gọi trực tiếp xuống repository để kiểm tra xem có thiết bị đang hoạt động nào khớp với fingerprint này không.
	// Cách này tối ưu hóa I/O vì repo chỉ trả về client_device_id dạng string chứ không quét nguyên cả struct Device cồng kềnh.
	return s.deviceRepo.GetActiveDeviceID(ctx, userID, fingerprint)
}

// [COMMENT]: Chuyển đổi zero-copy từ string sang slice byte để tránh allocations bộ nhớ phụ trên heap.
func unsafeStringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
