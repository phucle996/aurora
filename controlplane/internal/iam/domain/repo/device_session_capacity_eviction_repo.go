package iamRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

// DeviceSessionCapacityEvictionRepository owns the durable effect of ACR session-cap evictions.
type DeviceSessionCapacityEvictionRepository interface {
	Evict(ctx context.Context, userID uuid.UUID, clientDeviceIDs []uuid.UUID) error
}
