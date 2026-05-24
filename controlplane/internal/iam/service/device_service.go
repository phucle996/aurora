package iamSvcImpl

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	infraredis "controlplane/infra/redis"
	iamCache "controlplane/internal/iam/cache"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type DeviceService struct {
	deviceRepo       iamRepoInterface.DeviceRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	deviceRuntime    iamCache.UserDeviceRuntimeCache
	streamPublisher  infraredis.StreamPublisher
}

func NewDeviceService(deviceRepo iamRepoInterface.DeviceRepository, refreshTokenRepo iamRepoInterface.RefreshTokenRepository, deviceRuntime iamCache.UserDeviceRuntimeCache, streamPublisher infraredis.StreamPublisher) iamSvcInterface.DeviceService {
	return &DeviceService{deviceRepo: deviceRepo, refreshTokenRepo: refreshTokenRepo, deviceRuntime: deviceRuntime, streamPublisher: streamPublisher}
}

func (s *DeviceService) ListMyDevices(ctx context.Context, userID string, limit int, offset int) (*iamSvcInterface.DeviceListResult, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, listErr := s.deviceRepo.ListDevicesByUserID(ctx, uid, limit, offset)
	if listErr != nil {
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, listErr)
	}
	presenceByTracked := map[string]iamCache.UserDeviceRuntime{}
	if s.deviceRuntime != nil {
		runtimes, runtimeErr := s.deviceRuntime.ScanByUser(ctx, uid.String(), 200)
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
			p.IsOnline = strings.TrimSpace(rt.Status) != "revoked"
			if rt.LastSeenAt > 0 {
				ts := time.Unix(rt.LastSeenAt, 0).UTC()
				p.LastSeenAt = &ts
			}
			if v := strings.TrimSpace(rt.LastSeenIP); v != "" {
				p.LastIP = &v
			}
			if v := strings.TrimSpace(rt.LastSeenUserAgent); v != "" {
				p.LastUA = &v
			}
		}
		out = append(out, p)
	}
	return &iamSvcInterface.DeviceListResult{Devices: out, Total: int64(len(out))}, nil
}

func (s *DeviceService) RevokeMyDevice(ctx context.Context, userID string, deviceID string, ip *string, userAgent *string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
	}
	did, err := uuid.Parse(deviceID)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
	}
	_, getErr := s.deviceRepo.GetDeviceByIDAndUserID(ctx, did, uid)
	if getErr != nil {
		if errors.Is(getErr, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, getErr)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, getErr)
	}
	if revokeErr := s.deviceRepo.RevokeDeviceByIDAndUserID(ctx, did, uid); revokeErr != nil {
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, revokeErr)
	}
	_, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, uid, did)
	if revokeTokenErr != nil {
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, revokeTokenErr)
	}
	s.publishDeviceAudit(ctx, uid, "device.revoked", "warning", ip, userAgent, map[string]string{"target_device_id": strings.TrimSpace(deviceID)})
	return nil
}

func (s *DeviceService) LogoutOtherDevices(ctx context.Context, userID string, currentTrackedDeviceID string, ip *string, userAgent *string) (int64, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
	}
	var keep *uuid.UUID
	if currentTrackedDeviceID != "" {
		parsed, parseErr := uuid.Parse(currentTrackedDeviceID)
		if parseErr != nil {
			return 0, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseErr)
		}
		keep = &parsed
	}
	if _, revokeErr := s.deviceRepo.RevokeOtherDevicesByUserID(ctx, uid, keep); revokeErr != nil {
		return 0, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, revokeErr)
	}
	affected, revokeTokenErr := s.refreshTokenRepo.RevokeRefreshTokensByUserID(ctx, uid, keep)
	if revokeTokenErr != nil {
		return 0, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, revokeTokenErr)
	}
	s.publishDeviceAudit(ctx, uid, "device.logout_others", "warning", ip, userAgent, map[string]string{"affected_count": strconv.FormatInt(affected, 10)})
	return affected, nil
}

func (s *DeviceService) LogoutAllDevices(ctx context.Context, userID string, ip *string, userAgent *string) (int64, error) {
	return s.LogoutOtherDevices(ctx, userID, "", ip, userAgent)
}

// publishDeviceAudit publish device audit qua Redis stream với fallback DB.
func (s *DeviceService) publishDeviceAudit(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string) {
	if s == nil || s.deviceRepo == nil {
		return
	}
	if s.streamPublisher == nil {
		_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, event, severity, ip, userAgent)
		iamMetrics.ObserveAuditPublish(event, "fallback_db")
		return
	}
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
		iamMetrics.ObserveAuditPublish(event, "fallback_db")
		return
	}
	iamMetrics.ObserveAuditPublish(event, "published")
}
