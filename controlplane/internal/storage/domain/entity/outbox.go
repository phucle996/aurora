package storageEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: StorageOutboxStatus định nghĩa các trạng thái của một bản ghi outbox job.
type StorageOutboxStatus string

const (
	StorageOutboxStatusPending    StorageOutboxStatus = "PENDING"
	StorageOutboxStatusProcessing StorageOutboxStatus = "PROCESSING"
	StorageOutboxStatusSucceeded  StorageOutboxStatus = "SUCCEEDED"
	StorageOutboxStatusFailed     StorageOutboxStatus = "FAILED"
)

// [COMMENT]: StorageOwnerType định nghĩa kiểu dữ liệu an toàn (type safe) cho loại chủ sở hữu tài nguyên.
type StorageOwnerType string

const (
	StorageOwnerTypePersonal StorageOwnerType = "PERSONAL"
	StorageOwnerTypeTenant   StorageOwnerType = "TENANT"
)

// [COMMENT]: StorageOutboxRecord đại diện cho thực thể Transactional Outbox lưu lịch sử tác vụ bất đồng bộ.
type StorageOutboxRecord struct {
	ID      int64
	EventID uuid.UUID
	// ZoneID is the immutable runtime destination. Outbox routing is
	// zone-scoped; a generic scope string would allow ambiguous fan-out.
	ZoneID       uuid.UUID
	JobTopic     string
	Payload      []byte
	PayloadKeyID uuid.UUID
	OwnerID      uuid.UUID
	OwnerType    StorageOwnerType
	ActorUserID  *uuid.UUID
	Status       StorageOutboxStatus

	CompletedAt *time.Time
	JobVersion  uint32
	ResourceID  string
	// ResourceName is non-secret settlement metadata used after a successful
	// hard delete, when the business row no longer exists.
	ResourceName         string
	RollbackQuotaBytes   *int64
	PayloadSchemaVersion uint32
	TraceID              []byte
	Idle                 uint32
	ErrorCode            *string
	ErrorMessage         *string
}
