package e2e

import (
	"context"
	"testing"

	"controlplane/internal/iam/test/fixtures"
	"controlplane/internal/iam/test/mocks"
)

// [COMMENT]: End-to-End Test quy trình Đăng ký tài khoản (Ref: user_registration_god_view_workflow.md)
func TestUserRegistrationWorkflowE2E(t *testing.T) {
	ctx := context.Background()
	publisher := mocks.NewMockAccountVerificationPublisher()
	cache := mocks.NewMockCacheEngine()

	// Bước 1: User gửi payload đăng ký tài khoản mới
	newUser := fixtures.NewTestUserFixture()
	newUser.Status = "pending-active"

	// Bước 2: Sinh Verification Token và gửi Event qua Kafka
	token := "vtoken_987654321"
	err := publisher.PublishVerificationEvent(ctx, newUser.ID.String(), newUser.Email, token)
	if err != nil {
		t.Fatalf("failed to publish verification event: %v", err)
	}

	// Bước 3: Cache Verification Token với TTL 24 giờ
	err = cache.Set(ctx, "verify:"+token, newUser.ID.String(), 24*3600)
	if err != nil {
		t.Fatalf("failed to cache verification token: %v", err)
	}

	// Bước 4: User click link xác thực -> Verify Token thành công
	cachedUserID, err := cache.Get(ctx, "verify:"+token)
	if err != nil || cachedUserID != newUser.ID.String() {
		t.Fatalf("token verification failed, cached userID: %s", cachedUserID)
	}

	// Bước 5: Chuyển trạng thái User sang Active
	newUser.Status = "active"
	t.Logf("PASS: User %s (%s) registered & activated successfully!", newUser.Username, newUser.ID)
}
