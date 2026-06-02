package coreEntity

import "time"

type OutboxStatus string

const (
	OutboxStatusPending   OutboxStatus = "PENDING"
	OutboxStatusPublished OutboxStatus = "PUBLISHED"
	OutboxStatusFailed    OutboxStatus = "FAILED"
)

type OutboxRecord struct {
	ID          int64
	EventID     string
	Entity      string
	Op          string
	Payload     []byte
	Version     uint64
	Status      OutboxStatus
	Attempts    int
	LastAttempt *time.Time
	CreatedAt   time.Time
}
