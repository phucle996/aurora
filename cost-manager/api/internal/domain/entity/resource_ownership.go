package entity

import (
	"time"

	"github.com/google/uuid"
)

// ResourceOwnershipEventType định nghĩa loại thay đổi ảnh hưởng đến bảng projection sở hữu tài nguyên.
type ResourceOwnershipEventType string

const (
	ResourceOwnershipEventCreated ResourceOwnershipEventType = "RESOURCE_CREATED"
	ResourceOwnershipEventDeleted ResourceOwnershipEventType = "RESOURCE_DELETED"
)

// ResourceOwnershipEvent chứa các thuộc tính của sự kiện sở hữu tài nguyên phát ra từ Dataplane/Controlplane.
type ResourceOwnershipEvent struct {
	EventID        uuid.UUID
	ResourceType   string
	ResourceID     uuid.UUID
	ResourceName   string
	OwnerID        uuid.UUID
	OwnerType      string
	ZoneID         uuid.UUID
	SourceVersion  int64
	EffectiveAt    time.Time
	EventType      ResourceOwnershipEventType
	PayloadHashHex string
}
