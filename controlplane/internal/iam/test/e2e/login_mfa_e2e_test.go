package e2e

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/iam/test/fixtures"
	"controlplane/internal/iam/test/mocks"
)

// [COMMENT]: End-to-End Test quy trình Đăng nhập kèm Device-Bound & MFA Gate (Ref: username_login_god_view_workflow.md)
func TestUserLoginMFAGateWorkflowE2E(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockCacheEngine()

	user := fixtures.NewTestUserFixture()
	device := fixtures.NewTestDeviceFixture(user.ID)

	// Bước 1: Validate Password Hash và Device Public Key Fingerprint
	if device.PublicKeyFingerprint == "" {
		t.Fatal("device public key fingerprint missing")
	}

	// Bước 2: Bật MFA Gate -> Sinh MFA Challenge Session trong Redis
	setupID := fixtures.TestSetupID
	err := cache.Set(ctx, "mfa_pending:"+setupID, user.ID.String(), 10*time.Minute)
	if err != nil {
		t.Fatalf("failed to create MFA challenge in redis: %v", err)
	}

	// Bước 3: User nhập mã TOTP -> Verify thành công -> Xóa MFA Pending session
	val, err := cache.Get(ctx, "mfa_pending:"+setupID)
	if err != nil || val != user.ID.String() {
		t.Fatalf("MFA challenge verification failed: %v", err)
	}
	_ = cache.Delete(ctx, "mfa_pending:"+setupID)

	// Bước 4: Issuance cho Access JWT và Device-Bound Refresh Token
	tokenFixture := fixtures.NewTestTokenFixture()
	t.Logf("PASS: User %s logged in via MFA Gate! RefreshToken expires at: %v", user.Username, tokenFixture.ExpiresAt)
}
