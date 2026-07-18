package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: BillingOutboxRepository không biết subject NATS; mapping event_type thuộc relay để tránh dữ liệu DB điều khiển broker tùy ý.
type BillingOutboxRepository interface {
	Claim(ctx context.Context, limit int) ([]iamEntity.BillingOutboxEvent, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
	MarkDead(ctx context.Context, id int64, message string) error
}
