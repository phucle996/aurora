package storageModel

import (
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: StorageOutboxRecord đại diện cho mô hình dữ liệu ánh xạ trực tiếp bảng storage_outbox_records.
type StorageOutboxRecord struct {
	ID                   int64      `db:"id"`
	EventID              uuid.UUID  `db:"event_id"`
	ZoneID               uuid.UUID  `db:"zone_id"`
	JobTopic             string     `db:"job_topic"`
	Payload              []byte     `db:"payload"`
	OwnerID              uuid.UUID  `db:"owner_id"`
	OwnerType            string     `db:"owner_type"`
	ActorUserID          *uuid.UUID `db:"actor_user_id"`
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
		ZoneID:               e.ZoneID,
		JobTopic:             e.JobTopic,
		Payload:              e.Payload,
		OwnerID:              e.OwnerID,
		OwnerType:            string(e.OwnerType),
		ActorUserID:          e.ActorUserID,
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
