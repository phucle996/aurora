package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// DeviceRuntimeRevokeService owns the resource-first revoke workflow from its
// durable device mutation through to relay settlement.
type DeviceRuntimeRevokeService interface {
	RevokeDevice(ctx context.Context, userID uuid.UUID, clientDeviceID uuid.UUID, currentDeviceID uuid.UUID) error
	RevokeOtherDevices(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) (int64, error)
	Claim(ctx context.Context, limit int) ([]iamEntity.DeviceRuntimeRevokeOutboxEvent, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
	MarkDead(ctx context.Context, id int64, message string) error
}
