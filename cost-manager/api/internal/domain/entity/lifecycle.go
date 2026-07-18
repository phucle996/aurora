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
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: ResourceLifecycleEventType định nghĩa kiểu dữ liệu an toàn (type safe) cho loại sự kiện vòng đời tài nguyên.
type ResourceLifecycleEventType string

const (
	ResourceLifecycleEventCreated ResourceLifecycleEventType = "RESOURCE_CREATED"
	ResourceLifecycleEventDeleted ResourceLifecycleEventType = "RESOURCE_DELETED"
)

// [COMMENT]: LifecycleEvent chứa các thuộc tính sự kiện vòng đời tài nguyên phát ra từ Dataplane/Controlplane.
type LifecycleEvent struct {
	EventID        uuid.UUID
	ResourceType   string
	ResourceID     uuid.UUID
	ResourceName   string
	OwnerID        uuid.UUID
	OwnerType      string
	ZoneID         uuid.UUID
	SourceVersion  int64
	EffectiveAt    time.Time
	EventType      ResourceLifecycleEventType
	PayloadHashHex string
}
