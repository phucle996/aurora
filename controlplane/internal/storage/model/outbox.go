package storageModel

import (
	"time"

	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
)

// [COMMENT]: StorageOutboxRecord đại diện cho mô hình dữ liệu ánh xạ trực tiếp bảng storage_outbox_records.
type StorageOutboxRecord struct {
	ID                   int64      `db:"id"`
	EventID              uuid.UUID  `db:"event_id"`
	RoutingScope         string     `db:"routing_scope"`
	JobTopic             string     `db:"job_topic"`
	Payload              []byte     `db:"payload"`
	UserID               string     `db:"user_id"`
	Status               string     `db:"status"`
	CompletedAt          *time.Time `db:"completed_at"`
	JobVersion           uint32     `db:"job_version"`
	ResourceID           string     `db:"resource_id"`
	PayloadSchemaVersion uint32     `db:"payload_schema_version"`
	TraceID              []byte     `db:"trace_id"`
	Idle                 uint32     `db:"idle"`
	ErrorCode            *string    `db:"error_code"`
	ErrorMessage         *string    `db:"error_message"`
}

// [COMMENT]: OutboxEntityToModel chuyển đổi domain outbox entity sang db model.
func OutboxEntityToModel(e *storageEntity.StorageOutboxRecord) *StorageOutboxRecord {
	if e == nil {
		return nil
	}
	return &StorageOutboxRecord{
		ID:                   e.ID,
		EventID:              e.EventID,
		RoutingScope:         e.RoutingScope,
		JobTopic:             e.JobTopic,
		Payload:              e.Payload,
		UserID:               e.UserID,
		Status:               string(e.Status),
		CompletedAt:          e.CompletedAt,
		JobVersion:           e.JobVersion,
		ResourceID:           e.ResourceID,
		PayloadSchemaVersion: e.PayloadSchemaVersion,
		TraceID:              e.TraceID,
		Idle:                 e.Idle,
		ErrorCode:            e.ErrorCode,
		ErrorMessage:         e.ErrorMessage,
	}
}

// [COMMENT]: OutboxModelToEntity chuyển đổi db model sang domain outbox entity.
func OutboxModelToEntity(m *StorageOutboxRecord) *storageEntity.StorageOutboxRecord {
	if m == nil {
		return nil
	}
	return &storageEntity.StorageOutboxRecord{
		ID:                   m.ID,
		EventID:              m.EventID,
		RoutingScope:         m.RoutingScope,
		JobTopic:             m.JobTopic,
		Payload:              m.Payload,
		UserID:               m.UserID,
		Status:               storageEntity.StorageOutboxStatus(m.Status),
		CompletedAt:          m.CompletedAt,
		JobVersion:           m.JobVersion,
		ResourceID:           m.ResourceID,
		PayloadSchemaVersion: m.PayloadSchemaVersion,
		TraceID:              m.TraceID,
		Idle:                 m.Idle,
		ErrorCode:            m.ErrorCode,
		ErrorMessage:         m.ErrorMessage,
	}
}
