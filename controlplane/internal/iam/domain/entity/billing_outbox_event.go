package iamEntity

import "github.com/google/uuid"

// [COMMENT]: BillingOutboxEvent là claim generic; relay chỉ định tuyến event_type vào Shared Redis Stream nằm trong allowlist cố định.
type BillingOutboxEvent struct {
	ID        int64
	EventID   uuid.UUID
	EventType string
	OwnerID   uuid.UUID
	OwnerType string
	Payload   []byte
	Attempts  int
}
