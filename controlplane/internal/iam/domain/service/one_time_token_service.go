package iamSvcInterface

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type OneTimeTokenService interface {
	Issue(ctx context.Context, purpose string, userID, eventID uuid.UUID) (plainToken string, expiresAt time.Time, err error)
	Validate(ctx context.Context, purpose string, userID, eventID uuid.UUID, plainToken string) (valid bool, err error)
	Consume(ctx context.Context, purpose string, userID, eventID uuid.UUID, plainToken string) (consumed bool, err error)
}
