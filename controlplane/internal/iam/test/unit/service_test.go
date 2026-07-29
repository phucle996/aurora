package unit

import (
	"context"
	"testing"

	"controlplane/internal/iam/test/fixtures"
	"controlplane/internal/iam/test/mocks"
)

// [COMMENT]: Unit test cho việc khởi tạo Mocks và kiểm thử Publisher event xác thực
func TestAccountVerificationPublisherMock(t *testing.T) {
	pub := mocks.NewMockAccountVerificationPublisher()
	ctx := context.Background()

	user := fixtures.NewTestUserFixture()
	token := "verification_token_123456"

	err := pub.PublishVerificationEvent(ctx, user.ID.String(), user.Email, token)
	if err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	events := pub.GetPublishedEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(events))
	}

	if events[0]["email"] != user.Email {
		t.Errorf("expected email %q, got %v", user.Email, events[0]["email"])
	}
}

// [COMMENT]: Unit test cho MockCacheEngine hoạt động đúng chuẩn KV store
func TestMockCacheEngine(t *testing.T) {
	cache := mocks.NewMockCacheEngine()
	ctx := context.Background()

	key := "session:user:123"
	val := "token_data_active"

	if err := cache.Set(ctx, key, val, 0); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	got, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("failed to get cache: %v", err)
	}

	if got != val {
		t.Errorf("expected cache value %q, got %q", val, got)
	}
}
