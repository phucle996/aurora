package mailCache

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	"github.com/redis/go-redis/v9"
	"time"
)

type MailCache struct {
	rds *redis.Client
	ttl time.Duration
}

func NewMailCache(rds *redis.Client, cfg *config.Config) *MailCache {
	return &MailCache{
		rds: rds,
		ttl: cfg.Security.SecretCacheTTL,
	}
}

func (c *MailCache) GetTemplate(ctx context.Context, tenantID, id string) (*mailEntity.Template, error) {
	// Skeleton cache get
	return nil, nil
}

func (c *MailCache) SetTemplate(ctx context.Context, tenantID, id string, t *mailEntity.Template) error {
	// Skeleton cache set
	return nil
}

func (c *MailCache) InvalidateTemplate(ctx context.Context, tenantID, id string) error {
	// Skeleton cache delete
	return nil
}
