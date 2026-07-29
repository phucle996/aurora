package mocks

import (
	"context"
	"sync"
)

// [COMMENT]: MockAccountVerificationPublisher dùng giả lập Kafka producer gửi email xác thực tài khoản
type MockAccountVerificationPublisher struct {
	mu           sync.Mutex
	Published    []map[string]any
	PublishError error
}

func NewMockAccountVerificationPublisher() *MockAccountVerificationPublisher {
	return &MockAccountVerificationPublisher{
		Published: make([]map[string]any, 0),
	}
}

// [COMMENT]: Giả lập việc publish event lên Kafka topic
func (m *MockAccountVerificationPublisher) PublishVerificationEvent(ctx context.Context, userID, email, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PublishError != nil {
		return m.PublishError
	}

	m.Published = append(m.Published, map[string]any{
		"user_id": userID,
		"email":   email,
		"token":   token,
	})
	return nil
}

// GetPublishedEvents trả về danh sách các event đã được trigger
func (m *MockAccountVerificationPublisher) GetPublishedEvents() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]map[string]any, len(m.Published))
	for i, event := range m.Published {
		copied[i] = make(map[string]any, len(event))
		for key, value := range event {
			copied[i][key] = value
		}
	}
	return copied
}
