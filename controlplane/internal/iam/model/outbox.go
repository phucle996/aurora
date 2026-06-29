package iamModel

import (
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// IamOutboxRecord đại diện cho mô hình dữ liệu ánh xạ trực tiếp xuống PostgreSQL của bảng iam.iam_outbox_records
type IamOutboxRecord struct {
	ID                   int64      `db:"id"`
	EventID              uuid.UUID  `db:"event_id"`
	RoutingScope         string     `db:"routing_scope"`
	JobTopic             string     `db:"job_topic"`
	Payload              []byte     `db:"payload"` // Dữ liệu nhị phân serialized Protobuf
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

// IamOutboxEntityToModel chuyển đổi từ Domain Entity sang DB Model (tách biệt nghiệp vụ với lưu trữ)
func IamOutboxEntityToModel(e iamEntity.IamOutboxRecord) IamOutboxRecord {
	return IamOutboxRecord{
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

// IamOutboxModelToEntity chuyển đổi từ DB Model sang Domain Entity
func IamOutboxModelToEntity(m IamOutboxRecord) iamEntity.IamOutboxRecord {
	return iamEntity.IamOutboxRecord{
		ID:                   m.ID,
		EventID:              m.EventID,
		RoutingScope:         m.RoutingScope,
		JobTopic:             m.JobTopic,
		Payload:              m.Payload,
		UserID:               m.UserID,
		Status:               iamEntity.IamOutboxStatus(m.Status),
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
