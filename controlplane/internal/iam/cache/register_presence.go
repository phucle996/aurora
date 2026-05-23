package iamCache

import (
	"context"

	"controlplane/internal/security"
	"controlplane/pkg/id"

	goredis "github.com/redis/go-redis/v9"
)

const (
	registerUsernameBitmapKey = "iam:register:bitmap:username"
	registerEmailBitmapKey    = "iam:register:bitmap:email"
)

type RegisterPresenceCache interface {
	Check(ctx context.Context, username string, email string) (bool, bool, error)
	MarkExists(ctx context.Context, username string, email string) error
}

type redisRegisterPresenceCache struct {
	rdb *goredis.Client
}

func NewRegisterPresenceCache(rdb *goredis.Client) RegisterPresenceCache {
	if rdb == nil {
		return nil
	}
	return &redisRegisterPresenceCache{rdb: rdb}
}

func (c *redisRegisterPresenceCache) Check(ctx context.Context, username string, email string) (bool, bool, error) {
	usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", username)
	if err != nil {
		return false, false, err
	}
	emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", email)
	if err != nil {
		return false, false, err
	}

	usernameHit, err := c.rdb.GetBit(ctx, registerUsernameBitmapKey, id.BitmapIndex(usernameDigest)).Result()
	if err != nil {
		return false, false, err
	}
	emailHit, err := c.rdb.GetBit(ctx, registerEmailBitmapKey, id.BitmapIndex(emailDigest)).Result()
	if err != nil {
		return false, false, err
	}
	return usernameHit == 1, emailHit == 1, nil
}

func (c *redisRegisterPresenceCache) MarkExists(ctx context.Context, username string, email string) error {
	usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", username)
	if err != nil {
		return err
	}
	emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", email)
	if err != nil {
		return err
	}

	pipe := c.rdb.Pipeline()
	pipe.SetBit(ctx, registerUsernameBitmapKey, id.BitmapIndex(usernameDigest), 1)
	pipe.SetBit(ctx, registerEmailBitmapKey, id.BitmapIndex(emailDigest), 1)
	_, err = pipe.Exec(ctx)
	return err
}
