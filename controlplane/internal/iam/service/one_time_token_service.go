package iamSvcImpl

import (
	"context"
	"errors"
	"strings"
	"time"

	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
)

type OneTimeTokenService struct {
	cfg   *config.Config
	cache iamCache.OneTimeTokenCache
}

func NewOneTimeTokenService(cfg *config.Config, cacheStore iamCache.OneTimeTokenCache) iamSvcInterface.OneTimeTokenService {
	return &OneTimeTokenService{cfg: cfg, cache: cacheStore}
}

func (s *OneTimeTokenService) Issue(ctx context.Context, purpose string, userID string) (string, time.Time, error) {
	purpose = strings.TrimSpace(purpose)
	userID = strings.TrimSpace(userID)
	if purpose == "" || userID == "" {
		return "", time.Time{}, apperr.Wrap(iamErrorx.ErrOneTimeTokenInvalidPurposeOrUser, iamErrorx.ReasonOneTimeTokenInvalidPurposeOrUser, nil)
	}
	if s.cfg == nil || s.cfg.Security.OneTimeTokenTTL <= 0 {
		return "", time.Time{}, apperr.Wrap(iamErrorx.ErrOneTimeTokenIssueFailed, iamErrorx.ReasonOneTimeTokenIssueConfigError, nil)
	}

	rawToken, err := security.GenerateToken(43)
	if err != nil {
		return "", time.Time{}, apperr.Wrap(iamErrorx.ErrOneTimeTokenIssueFailed, iamErrorx.ReasonOneTimeTokenIssueDependencyError, err)
	}
	tokenHash := security.HashTokenSHA256(rawToken)
	if err := s.cache.SetHashedToken(ctx, purpose, userID, tokenHash, s.cfg.Security.OneTimeTokenTTL); err != nil {
		if errors.Is(err, iamErrorx.ErrOneTimeTokenCacheUnavailable) {
			return "", time.Time{}, apperr.Wrap(iamErrorx.ErrOneTimeTokenIssueFailed, iamErrorx.ReasonOneTimeTokenIssueDependencyError, err)
		}
		return "", time.Time{}, apperr.Wrap(iamErrorx.ErrOneTimeTokenIssueFailed, iamErrorx.ReasonOneTimeTokenIssueDependencyError, err)
	}

	expiresAt := time.Now().UTC().Add(s.cfg.Security.OneTimeTokenTTL)
	return rawToken, expiresAt, nil
}

func (s *OneTimeTokenService) Consume(ctx context.Context, purpose string, userID string, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	userID = strings.TrimSpace(userID)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == "" {
		return false, apperr.Wrap(iamErrorx.ErrOneTimeTokenInvalidPurposeOrUser, iamErrorx.ReasonOneTimeTokenInvalidPurposeOrUser, nil)
	}
	if plainToken == "" {
		return false, apperr.Wrap(iamErrorx.ErrOneTimeTokenInvalidOrExpired, iamErrorx.ReasonOneTimeTokenInvalidOrExpired, nil)
	}

	tokenHash := security.HashTokenSHA256(plainToken)
	consumed, err := s.cache.ConsumeHashedToken(ctx, purpose, userID, tokenHash)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrOneTimeTokenCacheUnavailable) {
			return false, apperr.Wrap(iamErrorx.ErrOneTimeTokenConsumeFailed, iamErrorx.ReasonOneTimeTokenConsumeDependencyErr, err)
		}
		return false, apperr.Wrap(iamErrorx.ErrOneTimeTokenConsumeFailed, iamErrorx.ReasonOneTimeTokenConsumeDependencyErr, err)
	}
	if !consumed {
		return false, apperr.Wrap(iamErrorx.ErrOneTimeTokenInvalidOrExpired, iamErrorx.ReasonOneTimeTokenInvalidOrExpired, nil)
	}
	return true, nil
}
