package iamSvcImpl

import (
	"context"
	"errors"
	"strings"
	"time"

	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

type OneTimeTokenService struct {
	cfg   *config.Config
	cache iamCache.OneTimeTokenCache
}

func NewOneTimeTokenService(cfg *config.Config, cacheStore iamCache.OneTimeTokenCache) iamSvcInterface.OneTimeTokenService {
	return &OneTimeTokenService{cfg: cfg, cache: cacheStore}
}

func (s *OneTimeTokenService) Issue(ctx context.Context, purpose string, userID uuid.UUID) (string, time.Time, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || userID == uuid.Nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_purpose_or_user")
	}
	if s.cfg == nil || s.cfg.Security.OneTimeTokenTTL <= 0 {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, nil, "config_error")
	}

	rawToken, err := security.GenerateToken(43)
	if err != nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "dependency_error")
	}
	tokenHash := security.HashTokenSHA256(rawToken)
	if err := s.cache.SetHashedToken(ctx, purpose, userID, tokenHash, s.cfg.Security.OneTimeTokenTTL); err != nil {
		if errors.Is(err, iamTaxonomy.ErrGetL1CacheFailed) {
			return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "cache_unavailable")
		}
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "cache_error")
	}

	expiresAt := time.Now().UTC().Add(s.cfg.Security.OneTimeTokenTTL)
	return rawToken, expiresAt, nil
}

func (s *OneTimeTokenService) Consume(ctx context.Context, purpose string, userID uuid.UUID, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == uuid.Nil {
		return false, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_purpose_or_user")
	}
	if plainToken == "" {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenRefreshExpired, nil, "invalid_or_expired")
	}

	tokenHash := security.HashTokenSHA256(plainToken)
	consumed, err := s.cache.ConsumeHashedToken(ctx, purpose, userID, tokenHash)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrGetL1CacheFailed) {
			return false, apperr.Wrap(iamTaxonomy.ErrTokenUpdateFailed, err, "cache_unavailable")
		}
		return false, apperr.Wrap(iamTaxonomy.ErrTokenUpdateFailed, err, "cache_error")
	}
	if !consumed {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenRefreshExpired, nil, "invalid_or_expired")
	}
	return true, nil
}
