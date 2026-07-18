package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type PersonalWalletProvisionOutboxRepository interface {
	Claim(ctx context.Context, limit int) ([]iamEntity.PersonalWalletProvisionEvent, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
	CleanupPublished(ctx context.Context, limit int) (int64, error)
}
