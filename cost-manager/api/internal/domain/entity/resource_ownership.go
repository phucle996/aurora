/*
============================================================================
MAP: BILLING DOMAIN ENTITY - LIFECYCLE EVENT
============================================================================
CONTRACT:
1. Định nghĩa thực thể LifecycleEvent lưu thông tin chuyển giao vòng đời tài nguyên (Resource Lifecycle Event).
============================================================================
*/

package entity

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrResourceOwnershipIntegrity = errors.New("resource ownership integrity violation")

// [COMMENT]: ResourceOwnershipEventType định nghĩa loại thay đổi ảnh hưởng ownership projection.
type ResourceOwnershipEventType string

const (
	ResourceOwnershipEventCreated ResourceOwnershipEventType = "RESOURCE_CREATED"
	ResourceOwnershipEventDeleted ResourceOwnershipEventType = "RESOURCE_DELETED"
)

// [COMMENT]: LifecycleEvent chứa các thuộc tính sự kiện vòng đời tài nguyên phát ra từ Dataplane/Controlplane.
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
