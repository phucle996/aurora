package mailModel

import (
	"encoding/json"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

// MailOutboxRecord đại diện cho mô hình dữ liệu ánh xạ trực tiếp xuống PostgreSQL
type MailOutboxRecord struct {
	ID                   int64           `db:"id"`
	EventID              uuid.UUID       `db:"event_id"`
	ZoneID               uuid.UUID       `db:"zone_id"`
	JobTopic             string          `db:"job_topic"`
	PayloadJSON          json.RawMessage `db:"payload_json"`
	Status               string          `db:"status"`
	Attempts             int             `db:"attempts"`
	LastAttempt          *time.Time      `db:"last_attempt"`
	CreatedAt            time.Time       `db:"created_at"`
	JobVersion           uint32          `db:"job_version"`
	ResourceID           string          `db:"resource_id"`
	PayloadSchemaVersion uint32          `db:"payload_schema_version"`
	TraceID              string          `db:"trace_id"`
	Idle                 uint32          `db:"idle"`
	ErrorCode            *string         `db:"error_code"`
	ErrorMessage         *string         `db:"error_message"`
}

// OutboxEntityToModel chuyển đổi từ Domain Entity sang DB Model
func OutboxEntityToModel(e mailEntity.MailOutboxRecord) MailOutboxRecord {
	return MailOutboxRecord{
		ID:                   e.ID,
		EventID:              e.EventID,
		ZoneID:               e.ZoneID,
		JobTopic:             e.JobTopic,
		PayloadJSON:          e.PayloadJSON,
		Status:               string(e.Status),
		Attempts:             e.Attempts,
		LastAttempt:          e.LastAttempt,
		CreatedAt:            e.CreatedAt,
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
		PayloadJSON:          m.PayloadJSON,
		Status:               mailEntity.OutboxStatus(m.Status),
		Attempts:             m.Attempts,
		LastAttempt:          m.LastAttempt,
		CreatedAt:            m.CreatedAt,
		JobVersion:           m.JobVersion,
		ResourceID:           m.ResourceID,
		PayloadSchemaVersion: m.PayloadSchemaVersion,
		TraceID:              m.TraceID,
		Idle:                 m.Idle,
		ErrorCode:            m.ErrorCode,
		ErrorMessage:         m.ErrorMessage,
	}
}

// MailJobPayload đại diện cho payload của công việc gửi thư (cần thiết cho cấu trúc JobPublisher xương cá)
type MailJobPayload struct {
	JobID   string
	Payload string
}
