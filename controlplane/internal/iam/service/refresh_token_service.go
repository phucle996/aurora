package iamSvcImpl

import (
	"context"
	"errors"
	"time"

	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/internal/iam/taxonomy"
	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
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
	refreshOutcome := iamTaxonomy.OutcomeSuccess
	defer func() {
		iamMetrics.ObserveServiceCall("refresh_token", refreshOutcome, "n/a")
	}()

	if rawRefreshToken == "" {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}

	refreshContext, ctxErr := s.repo.LoadRefreshContextByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if ctxErr != nil {
		if errors.Is(ctxErr, pgx.ErrNoRows) {
			refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, ctxErr, iamTaxonomy.RefreshOutcomeInvalidSession)
		}
		refreshOutcome = iamTaxonomy.RefreshOutcomeLoadSessionError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, ctxErr, iamTaxonomy.RefreshOutcomeLoadSessionError)
	}
	if refreshContext == nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	session := &refreshContext.Session
	if time.Now().UTC().After(session.ExpiresAt) {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	if session.DeviceID == nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	trackedDeviceID := *session.DeviceID

	user := &refreshContext.User
	if user.ID == (uuid.UUID{}) {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.RefreshOutcomeInvalidSession)
	}
	if s.registry == nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamTaxonomy.RefreshOutcomeIssueAccessError)
	}

	now := time.Now().UTC()

	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, idErr, iamTaxonomy.RefreshOutcomeIssueAccessError)
	}
	runtimeDeviceID := uuid.NewString()
	rawDeviceSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, secretErr, iamTaxonomy.RefreshOutcomeIssueAccessError)
	}
	accessExpiresAt := now.Add(s.cfg.Security.AccessSecretTTL)
	val, err := s.registry.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.RefreshOutcomeIssueAccessError)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, errors.New("invalid runtime secrets type"), refreshOutcome)
	}

	accessToken, accessErr := security.SignWithSecret(security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		AccessKey: runtimeDeviceID,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
	}, secrets.Active.Secret)
	if accessErr != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, accessErr, iamTaxonomy.RefreshOutcomeIssueAccessError)
	}

	rawNextRefreshToken, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeGenerateRefreshErr
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshErr, iamTaxonomy.RefreshOutcomeGenerateRefreshErr)
	}
	nextRefreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		refreshOutcome = iamTaxonomy.RefreshOutcomeGenerateRefreshErr
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshIDErr, iamTaxonomy.RefreshOutcomeGenerateRefreshErr)
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
			refreshOutcome = iamTaxonomy.RefreshOutcomeInvalidSession
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, rotateErr, iamTaxonomy.RefreshOutcomeInvalidSession)
		}
		refreshOutcome = iamTaxonomy.RefreshOutcomeRotateRefreshErr
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, rotateErr, iamTaxonomy.RefreshOutcomeRotateRefreshErr)
	}

	if s.deviceRuntime != nil {
		runtimeTTL := s.cfg.Security.AccessSecretTTL
		if runtimeTTL <= 0 {
			runtimeTTL = 15 * time.Minute
		}
		newSecretHash := security.HashTokenSHA256(rawNextRefreshToken[:0] + rawDeviceSecret)
		runtime := iamCache.UserDeviceRuntime{
			DeviceID:         runtimeDeviceID,
			DeviceSecretHash: newSecretHash,
			CurrentJTI:       accessJTI.String(),
			TrackedDeviceID:  trackedDeviceID.String(),
			UserID:           user.ID.String(),
			Status:           "online",
			Version:          1,
			LastSeenAt:       now.Unix(),
		}
		if setErr := s.deviceRuntime.SetDeviceRuntime(ctx, runtime, runtimeTTL); setErr != nil {
			refreshOutcome = iamTaxonomy.RefreshOutcomeIssueAccessError
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, setErr, iamTaxonomy.RefreshOutcomeIssueAccessError)
		}
	}

	return &iamEntity.RefreshTokenResult{
		AccessToken:      accessToken,
		RefreshToken:     rawNextRefreshToken,
		RuntimeDeviceID:  runtimeDeviceID,
		DeviceSecret:     rawDeviceSecret,
		TrackedDeviceID:  trackedDeviceID.String(),
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: nextRefreshExpiresAt,
	}, nil
}
