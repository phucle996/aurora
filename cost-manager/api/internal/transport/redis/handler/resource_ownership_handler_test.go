package handler

import (
	"context"
	"strings"
	"testing"

	"cost-manager/api/internal/config"
	"cost-manager/api/internal/domain/entity"
)

type mockOwnershipService struct {
	events []*entity.ResourceOwnershipEvent
}

func (m *mockOwnershipService) ProcessResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error {
	m.events = append(m.events, event)
	return nil
}

func TestNewResourceOwnershipConsumer(t *testing.T) {
	t.Parallel()

	mockSvc := &mockOwnershipService{}
	consumer := NewResourceOwnershipConsumer(nil, mockSvc)
	if consumer == nil {
		t.Fatal("expected non-nil consumer instance")
	}

	expectedPrefix := config.GetNodeHostname() + "-"
	if !strings.HasPrefix(consumer.consumer, expectedPrefix) {
		t.Fatalf("expected consumer identity to start with %q, got %q", expectedPrefix, consumer.consumer)
	}
}
