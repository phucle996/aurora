package iamEntity

import "github.com/google/uuid"

// LifecycleFactOutboxEvent is the flat delivery row for reviewed IAM/Hierarchy lifecycle facts.
type LifecycleFactOutboxEvent struct {
	ID        int64
	EventID   uuid.UUID
	EventType string
	OwnerID   uuid.UUID
	OwnerType string
	Payload   []byte
	Attempts  int
}
