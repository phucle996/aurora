package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// DevicePresenceProjectionRepository owns the durable presence projection.
type DevicePresenceProjectionRepository interface {
	Apply(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error
}
