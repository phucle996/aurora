package iamSvcImpl

import (
	"context"
	"errors"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RefreshTokenService struct {
	repo          iamRepoInterface.RefreshTokenRepository
	deviceRuntime iamCache.UserDeviceRuntimeCache
	registry      *cacheengine.CacheRegistry
	cfg           *config.Config
}

func NewRefreshTokenService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	deviceRuntime iamCache.UserDeviceRuntimeCache,
	registry *cacheengine.CacheRegistry,
) iamSvcInterface.RefreshTokenService {
	return &RefreshTokenService{
		repo:          repo,
		deviceRuntime: deviceRuntime,
		registry:      registry,
		cfg:           cfg,
	}
}

func (s *RefreshTokenService) Refresh(ctx context.Context, rawRefreshToken string) (result *iamEntity.RefreshTokenResult, err error) {
	refreshOutcome := iamTaxonomy.Success
	defer func() {
		iamMetrics.ServiceCall("refresh_token", string(refreshOutcome), "n/a")
	}()

	if rawRefreshToken == "" {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}

	refreshContext, ctxErr := s.repo.LoadRefreshContextByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if ctxErr != nil {
		if errors.Is(ctxErr, pgx.ErrNoRows) {
			refreshOutcome = iamTaxonomy.InvalidSession
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, ctxErr, iamTaxonomy.InvalidSession)
		}
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, ctxErr, iamTaxonomy.Failure)
	}
	if refreshContext == nil {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	session := &refreshContext.Session
	if time.Now().UTC().After(session.ExpiresAt) {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	if session.DeviceID == nil {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	trackedDeviceID := *session.DeviceID

	user := &refreshContext.User
	if user.ID == (uuid.UUID{}) {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		refreshOutcome = iamTaxonomy.InvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}
	if s.registry == nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamTaxonomy.Failure)
	}

	now := time.Now().UTC()

	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, idErr, iamTaxonomy.Failure)
	}
	accessKey := uuid.NewString()
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, secretErr, iamTaxonomy.Failure)
	}
	accessExpiresAt := now.Add(s.cfg.Security.AccessSecretTTL)
	val, err := s.registry.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, errors.New("invalid runtime secrets type"), refreshOutcome)
	}

	accessToken, accessErr := security.SignWithSecret(security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		AccessKey: accessKey,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
	}, secrets.Active.Secret)
	if accessErr != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, accessErr, iamTaxonomy.Failure)
	}

	rawNextRefreshToken, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshErr, iamTaxonomy.Failure)
	}
	nextRefreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshIDErr, iamTaxonomy.Failure)
	}
	nextRefreshExpiresAt := now.Add(s.cfg.Security.RefreshTokenTTL)
	nextRefreshToken := iamEntity.RefreshToken{
		ID:            nextRefreshID,
		UserID:        session.UserID,
		DeviceID:      &trackedDeviceID,
		TokenHash:     security.HashTokenSHA256(rawNextRefreshToken),
		TokenFamilyID: session.TokenFamilyID,
		TenantID:      nil,
		IssuedAt:      now,
		ExpiresAt:     nextRefreshExpiresAt,
	}

	if rotateErr := s.repo.RotateRefreshToken(ctx, *session, nextRefreshToken); rotateErr != nil {
		if errors.Is(rotateErr, iamTaxonomy.ErrInvalidSession) || errors.Is(rotateErr, pgx.ErrNoRows) {
			refreshOutcome = iamTaxonomy.InvalidSession
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, rotateErr, iamTaxonomy.InvalidSession)
		}
		refreshOutcome = iamTaxonomy.Failure
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, rotateErr, iamTaxonomy.Failure)
	}

	if s.deviceRuntime != nil {
		runtimeTTL := s.cfg.Security.AccessSecretTTL
		if runtimeTTL <= 0 {
			runtimeTTL = 15 * time.Minute
		}
		newAccessSecretHash := security.HashTokenSHA256(rawNextRefreshToken[:0] + accessSecret)
		runtime := iamCache.UserDeviceRuntime{
			AccessKey:        accessKey,
			AccessSecretHash: newAccessSecretHash,
			CurrentJTI:       accessJTI.String(),
			TrackedDeviceID:  trackedDeviceID.String(),
			UserID:           user.ID.String(),
			Status:           "online",
			Version:          1,
			LastSeenAt:       now.Unix(),
		}
		if setErr := s.deviceRuntime.SetDeviceRuntime(ctx, runtime, runtimeTTL); setErr != nil {
			refreshOutcome = iamTaxonomy.Failure
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, setErr, iamTaxonomy.Failure)
		}
	}

	return &iamEntity.RefreshTokenResult{
		AccessToken:      accessToken,
		RefreshToken:     rawNextRefreshToken,
		AccessKey:        accessKey,
		AccessSecret:     accessSecret,
		TrackedDeviceID:  trackedDeviceID.String(),
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: nextRefreshExpiresAt,
	}, nil
}
