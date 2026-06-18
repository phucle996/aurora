package mailModel

import (
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

// MailOutboxRecord đại diện cho mô hình dữ liệu ánh xạ trực tiếp xuống PostgreSQL
type MailOutboxRecord struct {
	ID       int64     `db:"id"`
	EventID  uuid.UUID `db:"event_id"`
	ZoneID   uuid.UUID `db:"zone_id"`
	JobTopic string    `db:"job_topic"`
	// Payload: Lưu trữ dạng nhị phân (Protobuf) cho công việc
	Payload []byte `db:"payload"`
	// UserID: ID người dùng thực thi công việc
	UserID               string     `db:"user_id"`
	Status               string     `db:"status"`
	CompletedAt          *time.Time `db:"completed_at"`
	JobVersion           uint32     `db:"job_version"`
	ResourceID           string     `db:"resource_id"`
	PayloadSchemaVersion uint32     `db:"payload_schema_version"`
	// TraceID: Lưu nhị phân 16 bytes thay vì string hex 32 bytes
	TraceID              []byte    `db:"trace_id"`
	Idle                 uint32     `db:"idle"`
	ErrorCode            *string    `db:"error_code"`
	ErrorMessage         *string    `db:"error_message"`
}

// OutboxEntityToModel chuyển đổi từ Domain Entity sang DB Model
func OutboxEntityToModel(e mailEntity.MailOutboxRecord) MailOutboxRecord {
	return MailOutboxRecord{
		ID:                   e.ID,
		EventID:              e.EventID,
		ZoneID:               e.ZoneID,
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

// OutboxModelToEntity chuyển đổi từ DB Model sang Domain Entity
func OutboxModelToEntity(m MailOutboxRecord) mailEntity.MailOutboxRecord {
	return mailEntity.MailOutboxRecord{
		ID:                   m.ID,
		EventID:              m.EventID,
		ZoneID:               m.ZoneID,
		JobTopic:             m.JobTopic,
		Payload:              m.Payload,
		UserID:               m.UserID,
		Status:               mailEntity.OutboxStatus(m.Status),
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
