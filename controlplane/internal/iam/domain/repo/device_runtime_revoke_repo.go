package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// DeviceRuntimeRevokeRepository owns one CTE mutation plus the lease lifecycle
// of its delivery records. It intentionally does not own device reads or login.
type DeviceRuntimeRevokeRepository interface {
	RevokeDevice(ctx context.Context, command iamEntity.DeviceRuntimeRevokeDevice) (iamEntity.DeviceRuntimeRevokeResult, error)
	RevokeOtherDevices(ctx context.Context, command iamEntity.DeviceRuntimeRevokeOthers) (int64, error)
	Claim(ctx context.Context, limit int) ([]iamEntity.DeviceRuntimeRevokeOutboxEvent, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
	MarkDead(ctx context.Context, id int64, message string) error
}
