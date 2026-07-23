package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// AccountVerificationDispatch là dữ liệu nghiệp vụ tối thiểu để yêu cầu gửi mail xác minh.
// Domain không biết topic, Kafka, Protobuf, Zone, consumer hay template runtime.
type AccountVerificationDispatch struct {
	EventID   uuid.UUID
	Recipient string
	Parameter map[string]string
	ExpiresAt time.Time
}
