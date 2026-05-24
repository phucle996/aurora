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
	iamErrorx "controlplane/internal/iam/errorx"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RefreshTokenService struct {
	repo          iamRepoInterface.RefreshTokenRepository
	deviceRuntime iamCache.UserDeviceRuntimeCache
	secrets       security.SecretProvider
	cfg           *config.Config
}

func NewRefreshTokenService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	deviceRuntime iamCache.UserDeviceRuntimeCache,
	secrets security.SecretProvider,
) iamSvcInterface.RefreshTokenService {
	return &RefreshTokenService{
		repo:          repo,
		deviceRuntime: deviceRuntime,
		secrets:       secrets,
		cfg:           cfg,
	}
}

func (s *RefreshTokenService) Refresh(ctx context.Context, rawRefreshToken string) (result *iamEntity.RefreshTokenResult, err error) {
	refreshOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ObserveRefreshTokenOutcome(refreshOutcome)
	}()

	if rawRefreshToken == "" {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}

	refreshContext, ctxErr := s.repo.LoadRefreshContextByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if ctxErr != nil {
		if errors.Is(ctxErr, pgx.ErrNoRows) {
			refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
			return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, ctxErr)
		}
		refreshOutcome = iamMetrics.RefreshOutcomeLoadSessionError
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, ctxErr)
	}
	if refreshContext == nil {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	session := &refreshContext.Session
	if time.Now().UTC().After(session.ExpiresAt) {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	if session.DeviceID == nil {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	trackedDeviceID := *session.DeviceID

	user := &refreshContext.User
	if user.ID == (uuid.UUID{}) {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
		return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, nil)
	}
	if s.secrets == nil {
		refreshOutcome = iamMetrics.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshAuthUnavailable, nil)
	}

	now := time.Now().UTC()

	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		refreshOutcome = iamMetrics.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshTokenIssue, idErr)
	}
	runtimeDeviceID := uuid.NewString()
	rawDeviceSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		refreshOutcome = iamMetrics.RefreshOutcomeIssueAccessError
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshTokenIssue, secretErr)
	}
	accessExpiresAt := now.Add(s.cfg.Security.AccessSecretTTL)
	accessToken, accessErr := security.Sign(ctx, s.secrets, security.SecretFamilyAccess, security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		DeviceID:  runtimeDeviceID,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
	})
	if accessErr != nil {
		refreshOutcome = iamMetrics.RefreshOutcomeIssueAccessError
		if errors.Is(accessErr, security.ErrEmptySecret) {
			return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshAuthUnavailable, accessErr)
		}
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshTokenIssue, accessErr)
	}

	rawNextRefreshToken, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		refreshOutcome = iamMetrics.RefreshOutcomeGenerateRefreshErr
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshTokenIssue, refreshErr)
	}
	nextRefreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		refreshOutcome = iamMetrics.RefreshOutcomeGenerateRefreshErr
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshTokenIssue, refreshIDErr)
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
		if errors.Is(rotateErr, iamErrorx.ErrInvalidSession) || errors.Is(rotateErr, pgx.ErrNoRows) {
			refreshOutcome = iamMetrics.RefreshOutcomeInvalidSession
			return nil, apperr.Wrap(iamErrorx.ErrInvalidSession, iamErrorx.ReasonRefreshInvalidSession, rotateErr)
		}
		refreshOutcome = iamMetrics.RefreshOutcomeRotateRefreshErr
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, rotateErr)
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
			refreshOutcome = iamMetrics.RefreshOutcomeIssueAccessError
			return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRefreshDependencyError, setErr)
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
