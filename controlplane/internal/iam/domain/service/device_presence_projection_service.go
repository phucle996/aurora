package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// DevicePresenceProjectionService applies normalized presence updates.
type DevicePresenceProjectionService interface {
	Apply(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error
}
