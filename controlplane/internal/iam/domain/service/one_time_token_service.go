package iamSvcInterface

import (
	"context"
	"time"
)

type OneTimeTokenService interface {
	Issue(ctx context.Context, purpose string, userID string) (plainToken string, expiresAt time.Time, err error)
	Consume(ctx context.Context, purpose string, userID string, plainToken string) (consumed bool, err error)
}
