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
	ID          int64
	EventID     string
	ZoneID      uuid.UUID
	JobTopic    string
	PayloadJSON string
	Status      OutboxStatus
	Attempts    int
	LastAttempt *time.Time
	CreatedAt   time.Time
}
