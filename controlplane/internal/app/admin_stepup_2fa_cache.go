package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	adminStepUp2FASettingsCacheKey = "iam:admin:2fa:settings:active"
	adminStepUp2FASettingsCacheTTL = 30 * time.Second
)

type adminStepUp2FASettingsCacheItem struct {
	SecretCiphertext  string `json:"secret_ciphertext"`
	UpdatedAtUnixNano int64  `json:"updated_at_unix_nano"`
}

// loadAdminStepUp2FASettings tối ưu source lookup cho critical step-up.
//
// Contract:
// - DB/IAM repository vẫn là source of truth dài hạn.
// - Redis chỉ cache ciphertext + updated_at ngắn hạn, không cache plaintext TOTP.
// - Redis lỗi hoặc cache hỏng thì fallback DB để cache phụ không làm chết flow.
func loadAdminStepUp2FASettings(
	ctx context.Context,
	rds *goredis.Client,
	loadFromSource func(ctx context.Context) (secretCiphertext string, updatedAt time.Time, err error),
) (string, time.Time, error) {
	if rds != nil {
		raw, err := rds.Get(ctx, adminStepUp2FASettingsCacheKey).Result()
		if err == nil {
			cached := adminStepUp2FASettingsCacheItem{}
			if json.Unmarshal([]byte(raw), &cached) == nil && strings.TrimSpace(cached.SecretCiphertext) != "" && cached.UpdatedAtUnixNano > 0 {
				return strings.TrimSpace(cached.SecretCiphertext), time.Unix(0, cached.UpdatedAtUnixNano).UTC(), nil
			}
		}
	}

	secretCiphertext, updatedAt, err := loadFromSource(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	secretCiphertext = strings.TrimSpace(secretCiphertext)
	if secretCiphertext == "" {
		return "", time.Time{}, nil
	}

	if rds != nil && !updatedAt.IsZero() {
		payload, marshalErr := json.Marshal(adminStepUp2FASettingsCacheItem{
			SecretCiphertext:  secretCiphertext,
			UpdatedAtUnixNano: updatedAt.UTC().UnixNano(),
		})
		if marshalErr == nil {
			_ = rds.Set(ctx, adminStepUp2FASettingsCacheKey, payload, adminStepUp2FASettingsCacheTTL).Err()
		}
	}

	return secretCiphertext, updatedAt, nil
}
