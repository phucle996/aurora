package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

type WalletAdmissionOutboxRepository interface {
	ClaimUnpublishedWalletAdmissionBatch(ctx context.Context, limit int, claimToken uuid.UUID) ([]*entity.WalletAdmissionOutboxRow, error)
	MarkWalletAdmissionPublished(ctx context.Context, eventID, claimToken uuid.UUID) error
	RecordWalletAdmissionError(ctx context.Context, eventID, claimToken uuid.UUID, message string) error
}
