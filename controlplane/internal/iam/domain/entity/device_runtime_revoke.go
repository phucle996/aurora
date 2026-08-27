package iamEntity

import "github.com/google/uuid"

// DeviceRuntimeRevokeDevice is the flat durable mutation for one non-current
// device. The event ID belongs to this execution attempt and is the outbox
// deduplication key.
type DeviceRuntimeRevokeDevice struct {
	EventID         uuid.UUID
	UserID          uuid.UUID
	ClientDeviceID  uuid.UUID
	CurrentDeviceID uuid.UUID
}

// DeviceRuntimeRevokeOthers is the flat durable mutation for every device
// except the currently authenticated device.
type DeviceRuntimeRevokeOthers struct {
	EventID         uuid.UUID
	UserID          uuid.UUID
	CurrentDeviceID uuid.UUID
}

// DeviceRuntimeRevokeResult is the durable mutation result. TargetExists and
// CurrentDevice are business facts consumed by the service, not transport data.
type DeviceRuntimeRevokeResult struct {
	TargetExists  bool
	CurrentDevice bool
	Affected      int64
}

// DeviceRuntimeRevokeOutboxEvent is the flat row claimed by the workflow-owned
// relay. Runtime session state remains in ACR; PostgreSQL stores only delivery
// intent until it crosses the Redis durability fence.
type DeviceRuntimeRevokeOutboxEvent struct {
	ID              int64
	EventID         uuid.UUID
	UserID          uuid.UUID
	ClientDeviceIDs []string
	Attempts        int
}
