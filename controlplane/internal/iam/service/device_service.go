package iamSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type DeviceService struct {
	deviceRepo       iamRepoInterface.DeviceRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	registry         *cacheengine.CacheRegistry
}

func NewDeviceService(deviceRepo iamRepoInterface.DeviceRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	registry *cacheengine.CacheRegistry,
) iamSvcInterface.DeviceService {
	return &DeviceService{
		deviceRepo:       deviceRepo,
		refreshTokenRepo: refreshTokenRepo,
		registry:         registry,
	}
}

func (s *DeviceService) getUserIDFromContext(ctx context.Context) (uuid.UUID, error) {
	ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity)
	if !ok || ident == nil || ident.UserID == "" {
		return uuid.Nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, errors.New("missing or invalid user identity in context"), "unauthorized")
	}
	uid, err := uuid.Parse(ident.UserID)
	if err != nil {
		return uuid.Nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
	}
	return uid, nil
}

func (s *DeviceService) ListMyDevices(ctx context.Context, limit int, offset int) (*iamSvcInterface.DeviceListResult, error) {
	userID, err := s.getUserIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, listErr := s.deviceRepo.ListDevicesByUserID(ctx, userID, limit, offset)
	if listErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}
	presenceByTracked := map[string]iamEntity.UserAccessSession{}
	if s.registry != nil {
		runtimes, runtimeErr := s.scanUserAccessSessions(ctx, s.registry.L2.Client(), userID.String(), 200)
		if runtimeErr == nil {
			for _, rt := range runtimes {
				key := strings.TrimSpace(rt.TrackedDeviceID)
				if key == "" {
					continue
				}
				presenceByTracked[key] = rt
			}
		}
	}
	out := make([]iamSvcInterface.DevicePresence, 0, len(items))
	for _, device := range items {
		p := iamSvcInterface.DevicePresence{Device: device, IsOnline: false}
		if rt, ok := presenceByTracked[strings.TrimSpace(device.ID)]; ok {
			p.IsOnline = true
			// LastSeenAt từ Redis phản ánh lần cuối request xác thực thành công (realtime qua middleware).
			if rt.LastSeenAt > 0 {
				ts := time.Unix(rt.LastSeenAt, 0).UTC()
				p.LastSeenAt = &ts
			}
		}
		out = append(out, p)
	}
	return &iamSvcInterface.DeviceListResult{Devices: out, Total: int64(len(out))}, nil
}

func (s *DeviceService) RevokeMyDevice(ctx context.Context, deviceID uuid.UUID) error {
	serviceOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, serviceOutcome)
	}()

	userID, err := s.getUserIDFromContext(ctx)
	if err != nil {
		serviceOutcome = iamMetrics.OutcomeFailureUnknown
		return err
	}

	// [COMMENT]: Đo lường thời gian thực thi câu lệnh SQL CTE (downstream).
	repoStart := time.Now()
	revokeErr := s.deviceRepo.RevokeDeviceByIDAndUserID(ctx, deviceID, userID)
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

	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeDeviceByIDAndUserID", iamMetrics.OutcomeSuccess, time.Since(repoStart), nil)
	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.revoked", "warning")
	return nil
}

func (s *DeviceService) LogoutOtherDevices(ctx context.Context, currentTrackedDeviceID *uuid.UUID) (int64, error) {
	userID, err := s.getUserIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	if _, revokeErr := s.deviceRepo.RevokeOtherDevicesByUserID(ctx, userID, currentTrackedDeviceID); revokeErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}
	affected, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByUserID(ctx, userID, currentTrackedDeviceID)
	if revokeTokenErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeTokenErr, "dependency_error")
	}
	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, "device.logout_others", "warning")
	iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
	return affected, nil
}

func (s *DeviceService) LogoutAllDevices(ctx context.Context) (int64, error) {
	return s.LogoutOtherDevices(ctx, nil)
}

func (s *DeviceService) scanUserAccessSessions(ctx context.Context, rdb redis.Cmdable, userID string, limit int) ([]iamEntity.UserAccessSession, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, iamTaxonomy.ErrUserAccessSessionInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	indexKey := "iam:user_access_index:" + userID
	keys := make([]string, 0, limit)
	var cursor uint64
	for len(keys) < limit {
		scanned, nextCursor, scanErr := rdb.SScan(ctx, indexKey, cursor, "*", int64(limit)).Result()
		if scanErr != nil {
			return nil, scanErr
		}
		for _, accessKey := range scanned {
			keys = append(keys, "iam:user_access_session:"+userID+":"+accessKey)
			if len(keys) >= limit {
				break
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return []iamEntity.UserAccessSession{}, nil
	}
	values, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]iamEntity.UserAccessSession, 0, len(values))
	for _, raw := range values {
		if raw == nil {
			continue
		}
		rawStr, ok := raw.(string)
		if !ok {
			continue
		}
		var pb iamproto.UserAccessSession
		if protoErr := proto.Unmarshal([]byte(rawStr), &pb); protoErr != nil {
			return nil, fmt.Errorf("iam cache: invalid user access session payload: %w", protoErr)
		}
		record := iamEntity.UserAccessSession{
			AccessSecretHash: pb.Ash,
			TrackedDeviceID:  pb.Tdid,
			LastSeenAt:       pb.Lsa,
		}
		out = append(out, record)
	}
	return out, nil
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

	// Runtime cleanup là side-effect best-effort sau DB evict thành công.
	type sessionWithKey struct {
		key    string
		record iamEntity.UserAccessSession
	}
	indexKey := "iam:user_access_index:" + userIDStr
	var runtimes []sessionWithKey
	var cursor uint64
	for len(runtimes) < 200 {
		scanned, nextCursor, scanErr := rdb.SScan(ctx, indexKey, cursor, "*", 200).Result()
		if scanErr != nil {
			break
		}
		keys := make([]string, 0, len(scanned))
		for _, aKey := range scanned {
			keys = append(keys, "iam:user_access_session:"+userIDStr+":"+aKey)
		}
		if len(keys) > 0 {
			if values, mgetErr := rdb.MGet(ctx, keys...).Result(); mgetErr == nil {
				for i, raw := range values {
					if raw == nil {
						continue
					}
					if rawStr, ok := raw.(string); ok {
						var pb iamproto.UserAccessSession
						if proto.Unmarshal([]byte(rawStr), &pb) == nil {
							record := iamEntity.UserAccessSession{
								AccessSecretHash: pb.Ash,
								TrackedDeviceID:  pb.Tdid,
								LastSeenAt:       pb.Lsa,
							}
							runtimes = append(runtimes, sessionWithKey{
								key:    scanned[i],
								record: record,
							})
						}
					}
				}
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	evictedRefs := make(map[string]struct{}, len(evicted))
	for _, item := range evicted {
		evictedRefs[strings.TrimSpace(item.DeviceID.String())] = struct{}{}
	}
	for _, rt := range runtimes {
		if _, found := evictedRefs[strings.TrimSpace(rt.record.TrackedDeviceID)]; found {
			delKey := "iam:user_access_session:" + userIDStr + ":" + rt.key
			delIndexKey := "iam:user_access_index:" + userIDStr
			pipe := rdb.TxPipeline()
			pipe.Del(ctx, delKey)
			pipe.SRem(ctx, delIndexKey, rt.key)
			_, _ = pipe.Exec(ctx)
		}
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
