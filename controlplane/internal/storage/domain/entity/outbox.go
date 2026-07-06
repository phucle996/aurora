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

// [COMMENT]: StorageOutboxRecord đại diện cho thực thể Transactional Outbox lưu lịch sử tác vụ bất đồng bộ.
type StorageOutboxRecord struct {
	ID                   int64
	EventID              uuid.UUID
	RoutingScope         string
	JobTopic             string
	Payload              []byte
	UserID               string
	Status               StorageOutboxStatus
	CompletedAt          *time.Time
	JobVersion           uint32
	ResourceID           string
	PayloadSchemaVersion uint32
	TraceID              []byte
	Idle                 uint32
	ErrorCode            *string
	ErrorMessage         *string
}
