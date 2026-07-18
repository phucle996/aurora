package iamSvcImpl

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

func (s *OneTimeTokenService) Validate(ctx context.Context, purpose string, userID uuid.UUID, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == uuid.Nil || plainToken == "" {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}
	storedHash, err := s.cacheEngine.L2.Client().Get(ctx, oneTimeTokenKey(purpose, userID)).Result()
	if err != nil {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, err, "invalid_or_expired")
	}
	expectedHash := security.HashTokenSHA256(plainToken)
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) == 1, nil
}

type OneTimeTokenService struct {
	cfg         *config.Config
	cacheEngine *cacheengine.CacheRegistry
}

func NewOneTimeTokenService(cfg *config.Config, cacheEngine *cacheengine.CacheRegistry) iamSvcInterface.OneTimeTokenService {
	return &OneTimeTokenService{cfg: cfg, cacheEngine: cacheEngine}
}

func oneTimeTokenKey(purpose string, userID uuid.UUID) string {
	return fmt.Sprintf("iam:ott:%s:%s", strings.TrimSpace(purpose), strings.TrimSpace(userID.String()))
}

func (s *OneTimeTokenService) Issue(ctx context.Context, purpose string, userID uuid.UUID) (string, time.Time, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || userID == uuid.Nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_purpose_or_user")
	}
	if s.cfg == nil || s.cfg.Security.OneTimeTokenTTL <= 0 {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, nil, "config_error")
	}
	if s.cacheEngine == nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, errors.New("cache engine unavailable"), "cache_unavailable")
	}

	rawToken, err := security.GenerateToken(43)
	if err != nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "dependency_error")
	}
	tokenHash := security.HashTokenSHA256(rawToken)
	key := oneTimeTokenKey(purpose, userID)

	if err := s.cacheEngine.L2.Client().Set(ctx, key, tokenHash, s.cfg.Security.OneTimeTokenTTL).Err(); err != nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "cache_unavailable")
	}

	expiresAt := time.Now().UTC().Add(s.cfg.Security.OneTimeTokenTTL)
	return rawToken, expiresAt, nil
}

var consumeOneTimeTokenScript = `
local key = KEYS[1]
local expected = ARGV[1]
local current = redis.call("GET", key)
if not current then
  return 0
end
if current ~= expected then
  return 0
end
return redis.call("DEL", key)
`

func (s *OneTimeTokenService) Consume(ctx context.Context, purpose string, userID uuid.UUID, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == uuid.Nil {
		return false, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_purpose_or_user")
	}
	if plainToken == "" {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}

	tokenHash := security.HashTokenSHA256(plainToken)
	key := oneTimeTokenKey(purpose, userID)

	resVal, err := s.cacheEngine.Exec.Execute(ctx, consumeOneTimeTokenScript, []string{key}, tokenHash)
	if err != nil {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenConsume, err, "cache_unavailable")
	}

	result, _ := resVal.(int64)
	if result != 1 {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}
	return true, nil
}
