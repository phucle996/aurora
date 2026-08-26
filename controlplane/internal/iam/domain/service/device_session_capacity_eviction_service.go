package iamSvcInterface

import (
	"context"

	"github.com/google/uuid"
)

// DeviceSessionCapacityEvictionService applies the durable side of ACR session-cap enforcement.
type DeviceSessionCapacityEvictionService interface {
	Evict(ctx context.Context, userID uuid.UUID, clientDeviceIDs []uuid.UUID) error
}
