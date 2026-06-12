package mailSvcImpl

import (
	"context"
	"fmt"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	"controlplane/pkg/logger"

	"github.com/redis/go-redis/v9"
)

type MailOutboxPoller struct {
	cfg    *config.Config
	repo   mailRepoInterface.MailOutboxRepository
	rdsJob *redis.Client
}

func NewMailOutboxPoller(
	cfg *config.Config,
	repo mailRepoInterface.MailOutboxRepository,
	rdsJob *redis.Client,
) *MailOutboxPoller {
	return &MailOutboxPoller{
		cfg:    cfg,
		repo:   repo,
		rdsJob: rdsJob,
	}
}

func (p *MailOutboxPoller) Start(ctx context.Context) {
	logger.SysInfo("mail.outbox", "Starting MailOutboxPoller background worker...")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Subscribe to mail:outbox:trigger on the Redis Job client
	pubsub := p.rdsJob.Subscribe(ctx, "mail:outbox:trigger")
	defer pubsub.Close()

	ch := pubsub.Channel()

	// Initial run on startup
	p.processPending(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.SysInfo("mail.outbox", "MailOutboxPoller context cancelled. Stopping worker gracefully...")
			return
		case msg := <-ch:
			if msg != nil {
				p.processPending(ctx)
			}
		case <-ticker.C:
			p.processPending(ctx)
		}
	}
}

func (p *MailOutboxPoller) processPending(ctx context.Context) {
	const limit = 20
	records, err := p.repo.FetchPendingForUpdate(ctx, limit)
	if err != nil {
		logger.SysError("mail.outbox", fmt.Sprintf("Failed to fetch pending outbox records: %v", err))
		return
	}

	if len(records) == 0 {
		return
	}

	for _, rec := range records {
		if err := p.publishToRedisStream(ctx, rec); err != nil {
			logger.SysWarnFields("mail.outbox", "Failed to publish outbox event to Redis job stream", err, logger.Fields{
				"event_id": rec.EventID,
				"topic":    rec.JobTopic,
			})
			_ = p.repo.MarkFailed(ctx, rec.ID, err.Error())
			continue
		}

		if err := p.repo.MarkPublished(ctx, rec.ID); err != nil {
			logger.SysErrorFields("mail.outbox", "Failed to mark outbox event as published in database", err, logger.Fields{
				"event_id": rec.EventID,
			})
		}
	}
}

func (p *MailOutboxPoller) publishToRedisStream(ctx context.Context, rec *mailEntity.MailOutboxRecord) error {
	// Stream key is always jobs:<zone_id>
	streamKey := fmt.Sprintf("jobs:%s", rec.ZoneID.String())
	createdAtStr := rec.CreatedAt.UTC().Format(time.RFC3339)
	deadlineAtStr := rec.CreatedAt.Add(15 * time.Second).UTC().Format(time.RFC3339)

	// XADD jobs:<zone_id>
	values := map[string]interface{}{
		"job_id":                 rec.EventID,
		"job_version":            "1",
		"attempt":                "1",
		"zone":                   rec.ZoneID.String(),
		"job_topic":              rec.JobTopic,
		"resource_id":            "transient_test",
		"payload_schema_version": "1",
		"payload_json":           rec.PayloadJSON,
		"trace_id":               "trace-" + rec.EventID.String(), // Transient trace ID based on event ID
		"created_at":             createdAtStr,
		"deadline_at":            deadlineAtStr,
	}

	err := p.rdsJob.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: values,
	}).Err()

	return err
}
