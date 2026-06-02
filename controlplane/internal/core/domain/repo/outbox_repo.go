package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"
)

type OutboxRepository interface {
	Save(ctx context.Context, record *coreEntity.OutboxRecord) error
	FetchPending(ctx context.Context, limit int) ([]*coreEntity.OutboxRecord, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, reason string) error
}
