package mailCache

import (
	"context"
	"controlplane/internal/config"
	mailModel "controlplane/internal/mail/model"
	"github.com/redis/go-redis/v9"
)

type JobPublisher struct {
	rds *redis.Client
	cfg *config.Config
}

func NewJobPublisher(rds *redis.Client, cfg *config.Config) *JobPublisher {
	return &JobPublisher{
		rds: rds,
		cfg: cfg,
	}
}

func (p *JobPublisher) PublishMailJob(ctx context.Context, job *mailModel.MailJobPayload) error {
	// Skeleton job enqueueing logic (e.g. LPUSH mail:jobs:queue)
	return nil
}
