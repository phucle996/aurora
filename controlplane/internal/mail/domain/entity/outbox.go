package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

type OutboxStatus string

const (
	OutboxStatusPending   OutboxStatus = "PENDING"
	OutboxStatusPublished OutboxStatus = "PUBLISHED"
	OutboxStatusFailed    OutboxStatus = "FAILED"
)

type MailOutboxRecord struct {
	ID                 int64
	EventID            uuid.UUID
	ZoneID             uuid.UUID
	JobTopic           string
	PayloadJSON        string
	Status             OutboxStatus
	Attempts           int
	LastAttempt        *time.Time
	CreatedAt          time.Time
	JobVersion         uint32
	ResourceID         string
	PayloadSchemaVersion uint32
	TraceID            string
	Idle               uint32
	ErrorCode          *string
	ErrorMessage       *string
}

