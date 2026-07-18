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
	"github.com/redis/go-redis/v9"
)

func (s *OneTimeTokenService) Validate(ctx context.Context, purpose string, userID, eventID uuid.UUID, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == uuid.Nil || eventID == uuid.Nil || plainToken == "" {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}
	storedHash, err := s.cacheEngine.L2.Client().Get(ctx, oneTimeTokenKey(purpose, userID, eventID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}
	if err != nil {
		// [COMMENT]: Redis outage là dependency failure, không được giả dạng token hết hạn do người dùng.
		return false, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "cache_unavailable")
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

func oneTimeTokenKey(purpose string, userID, eventID uuid.UUID) string {
	// [COMMENT]: event_id tách từng email, nên mail đến đảo thứ tự không vô hiệu hóa link còn TTL.
	return fmt.Sprintf("iam:ott:%s:%s:%s", strings.TrimSpace(purpose), userID.String(), eventID.String())
}

func (s *OneTimeTokenService) Issue(ctx context.Context, purpose string, userID, eventID uuid.UUID) (string, time.Time, error) {
	purpose = strings.TrimSpace(purpose)
	if s.cfg == nil || purpose == "" || userID == uuid.Nil || eventID == uuid.Nil || s.cfg.Security.OneTimeTokenTTL <= 0 ||
		s.cfg.Security.OneTimeTokenReplicaAcks < 0 ||
		(s.cfg.Security.OneTimeTokenReplicaAcks > 0 && s.cfg.Security.OneTimeTokenReplicaWait <= 0) {
		// [COMMENT]: Config durability sai phải fail closed, không âm thầm biến ACK âm thành chế độ single-node.
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, nil, "invalid_ott_configuration")
	}

	rawToken, err := security.GenerateToken(43)
	if err != nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "dependency_error")
	}
	tokenHash := security.HashTokenSHA256(rawToken)
	key := oneTimeTokenKey(purpose, userID, eventID)

	// [COMMENT]: Redis WAIT chỉ bảo đảm các write trước đó trên cùng client connection.
	// Giữ dedicated connection cho cả SET và WAIT để pool không làm ACK nhầm replication offset.
	conn := s.cacheEngine.L2.Client().Conn()
	defer conn.Close()
	if err := conn.Set(ctx, key, tokenHash, s.cfg.Security.OneTimeTokenTTL).Err(); err != nil {
		return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, err, "cache_unavailable")
	}
	if required := s.cfg.Security.OneTimeTokenReplicaAcks; required > 0 {
		acked, waitErr := conn.Wait(ctx, required, s.cfg.Security.OneTimeTokenReplicaWait).Result()
		if waitErr != nil || int(acked) < required {
			// [COMMENT]: Fail closed trước DB commit; key primary-only bị xóa best-effort để tránh phát mail không đạt durability gate.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = conn.Del(cleanupCtx, key).Err()
			cleanupCancel()
			replicationErr := waitErr
			if replicationErr == nil {
				replicationErr = fmt.Errorf("redis replication ack %d/%d", acked, required)
			}
			return "", time.Time{}, apperr.Wrap(iamTaxonomy.ErrTokenIssueFailed, replicationErr, "replication_unavailable")
		}
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

func (s *OneTimeTokenService) Consume(ctx context.Context, purpose string, userID, eventID uuid.UUID, plainToken string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	plainToken = strings.TrimSpace(plainToken)
	if purpose == "" || userID == uuid.Nil || eventID == uuid.Nil {
		return false, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_purpose_or_user")
	}
	if plainToken == "" {
		return false, apperr.Wrap(iamTaxonomy.ErrTokenExpired, nil, "invalid_or_expired")
	}

	tokenHash := security.HashTokenSHA256(plainToken)
	key := oneTimeTokenKey(purpose, userID, eventID)

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
