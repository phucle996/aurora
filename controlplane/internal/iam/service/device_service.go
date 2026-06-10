package iamSvcImpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraredis "controlplane/infra/redis"
	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type DeviceService struct {
	deviceRepo       iamRepoInterface.DeviceRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	registry         *cacheengine.CacheRegistry
	streamPublisher  infraredis.StreamPublisher
}

func NewDeviceService(deviceRepo iamRepoInterface.DeviceRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	registry *cacheengine.CacheRegistry,
	streamPublisher infraredis.StreamPublisher,
) iamSvcInterface.DeviceService {
	return &DeviceService{deviceRepo: deviceRepo,
		refreshTokenRepo: refreshTokenRepo,
		registry:         registry,
		streamPublisher:  streamPublisher}
}

func (s *DeviceService) ListMyDevices(ctx context.Context, userID string, limit int, offset int) (*iamSvcInterface.DeviceListResult, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, listErr := s.deviceRepo.ListDevicesByUserID(ctx, uid, limit, offset)
	if listErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}
	presenceByTracked := map[string]iamEntity.UserAccessSession{}
	if s.registry != nil {
		runtimes, runtimeErr := s.scanUserAccessSessions(ctx, s.registry.L2.Client(), uid.String(), 200)
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

func (s *DeviceService) RevokeMyDevice(ctx context.Context, userID string, deviceID string, ip *string, userAgent *string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
	}
	did, err := uuid.Parse(deviceID)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
	}
	_, getErr := s.deviceRepo.GetDeviceByIDAndUserID(ctx, did, uid)
	if getErr != nil {
		if errors.Is(getErr, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidSession, getErr, "invalid_session")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, getErr, "dependency_error")
	}
	if revokeErr := s.deviceRepo.RevokeDeviceByIDAndUserID(ctx, did, uid); revokeErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}
	_, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, uid, did)
	if revokeTokenErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeTokenErr, "dependency_error")
	}
	s.publishDeviceAudit(ctx, uid, "device.revoked", "warning", ip, userAgent, map[string]string{"target_device_id": strings.TrimSpace(deviceID)})
	return nil
}

func (s *DeviceService) LogoutOtherDevices(ctx context.Context, userID string, currentTrackedDeviceID string, ip *string, userAgent *string) (int64, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
	}
	var keep *uuid.UUID
	if currentTrackedDeviceID != "" {
		parsed, parseErr := uuid.Parse(currentTrackedDeviceID)
		if parseErr != nil {
			return 0, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseErr, "invalid_argument")
		}
		keep = &parsed
	}
	if _, revokeErr := s.deviceRepo.RevokeOtherDevicesByUserID(ctx, uid, keep); revokeErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeErr, "dependency_error")
	}
	affected, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByUserID(ctx, uid, keep)
	if revokeTokenErr != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, revokeTokenErr, "dependency_error")
	}
	s.publishDeviceAudit(ctx, uid, "device.logout_others", "warning", ip, userAgent, map[string]string{"affected_count": strconv.FormatInt(affected, 10)})
	return affected, nil
}

func (s *DeviceService) LogoutAllDevices(ctx context.Context, userID string, ip *string, userAgent *string) (int64, error) {
	return s.LogoutOtherDevices(ctx, userID, "", ip, userAgent)
}

// publishDeviceAudit publish device audit qua Redis stream với fallback DB.
func (s *DeviceService) publishDeviceAudit(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string) {

	_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, event, severity, ip, userAgent)
	iamMetrics.ServiceCall("audit_publish", "fallback_db", "n/a")
	now := time.Now().UTC()
	payload := map[string]string{
		"event":        event,
		"severity":     severity,
		"user_id":      userID.String(),
		"published_at": now.Format(time.RFC3339Nano),
	}
	if ip != nil {
		payload["ip"] = strings.TrimSpace(*ip)
	}
	if userAgent != nil {
		payload["user_agent"] = strings.TrimSpace(*userAgent)
	}
	for k, v := range extras {
		payload[k] = v
	}
	msg := infraredis.StreamMessage{
		Stream:         "iam:audit:device",
		IdempotencyKey: userID.String() + ":" + event,
		Payload:        payload,
	}
	if _, _, err := s.streamPublisher.Publish(ctx, msg, 30*time.Second); err != nil {
		_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, event, severity, ip, userAgent)
		iamMetrics.ServiceCall("audit_publish", "fallback_db", "n/a")
		return
	}
	iamMetrics.ServiceCall("audit_publish", "published", "n/a")
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
		var record iamEntity.UserAccessSession
		if jsonErr := json.Unmarshal([]byte(rawStr), &record); jsonErr != nil {
			return nil, fmt.Errorf("iam cache: invalid user access session payload: %w", jsonErr)
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *DeviceService) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return s.deviceRepo.UpsertLoginDevice(ctx, device)
}

func (s *DeviceService) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error {
	return s.deviceRepo.TouchDeviceLastSeen(ctx, deviceID, ip, userAgent)
}

func (s *DeviceService) EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID, ip *string, userAgent *string) {
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
		iamMetrics.ServiceCall("device_cap_lock", "skip", "n/a")
	} else if !ok {
		iamMetrics.ServiceCall("device_cap_lock", "skip", "n/a")
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
						var record iamEntity.UserAccessSession
						if json.Unmarshal([]byte(rawStr), &record) == nil {
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

	iamMetrics.ServiceCall("device_cap_evict", "evicted", "n/a")
	extras := map[string]string{
		"reason":        "cap_exceeded",
		"evicted_count": strconv.Itoa(len(evicted)),
	}
	s.PublishDeviceAuditAsync(ctx, userID, "device.evicted_capacity", "warning", ip, userAgent, extras)
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
		s.EvictExcessDevicesIfNeeded(ctx, userID, nil, nil)
	}
	return len(users), nil
}

func (s *DeviceService) PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string) {
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.publishDeviceAudit(bgCtx, userID, event, severity, ip, userAgent, extras)
	}()
}
