package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// LifecycleFactOutboxRepository owns only lease and settlement; transport owns the fixed stream allowlist.
type LifecycleFactOutboxRepository interface {
	Claim(ctx context.Context, limit int) ([]iamEntity.LifecycleFactOutboxEvent, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
	MarkDead(ctx context.Context, id int64, message string) error
}
