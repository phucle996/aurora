package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

type SessionRefreshService struct {
	repo    iamRepoInterface.RefreshTokenRepository
	cfg     *config.Config
	metrics observability.WorkflowRecorder
}

func NewSessionRefreshService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.SessionRefreshService {
	return &SessionRefreshService{repo: repo, cfg: cfg, metrics: metrics}
}

func (s *SessionRefreshService) IssueDeviceRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	rawRefresh, err := security.GenerateToken(128)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: generate credential: %w", err)
	}
	refreshID, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: generate credential ID: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.cfg.Security.RefreshTokenTTL)
	if err := s.repo.IssueDeviceRefreshToken(ctx, &iamEntity.IssueDeviceRefreshToken{
		ID: refreshID, UserID: userID, DeviceID: deviceID,
		TokenHash: security.HashTokenSHA256(rawRefresh), IssuedAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		if errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, err, "unauthenticated")
		}
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "internal")
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return rawRefresh, expiresAt, nil
}

func (s *SessionRefreshService) RecoverUserSession(ctx context.Context, rawRefreshToken string, requestedTenantID *uuid.UUID) (*iamEntity.RecoverUserSession, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	recovered, err := s.repo.RecoverUserSession(ctx, &iamEntity.RecoverUserSession{
		TokenHash: security.HashTokenSHA256(rawRefreshToken), RequestedTenantID: requestedTenantID, Now: time.Now().UTC(),
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidCredential):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return recovered, nil
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
			return recovered, nil
		default:
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "internal")
		}
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return recovered, nil
}

func (s *SessionRefreshService) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	_, err := s.repo.DeleteByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if err != nil && !errors.Is(err, iamTaxonomy.ErrNotFound) {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "internal")
	}

	// Revocation is idempotent: an already absent opaque credential is the desired state.
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
