package mocks

import (
	"context"
	"sync"
	"time"
)

// [COMMENT]: MockCacheEngine giả lập bộ nhớ đệm In-Memory Redis cho unit tests
type MockCacheEngine struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMockCacheEngine() *MockCacheEngine {
	return &MockCacheEngine{
		data: make(map[string]string),
	}
}

// [COMMENT]: Ghi dữ liệu cache có TTL
func (m *MockCacheEngine) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

// [COMMENT]: Lấy dữ liệu từ cache
func (m *MockCacheEngine) Get(ctx context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return val, nil
}

// [COMMENT]: Xóa dữ liệu cache
func (m *MockCacheEngine) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
