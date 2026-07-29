package fixtures

import (
	"time"
)

// [COMMENT]: Constants và Fixtures cho JWT Tokens, Refresh Tokens, và MFA Secrets
const (
	TestRawRefreshToken = "rt_mock_secure_random_token_string_64_bytes_long_hash_test"
	TestTOTPSecret      = "JBSWY3DPEHPK3PXP"
	TestSetupID         = "22222222-2222-2222-2222-222222222222"
)

// TestTokenFixture lưu trữ thông tin token giả lập dùng kiểm thử xoay vòng refresh token
type TestTokenFixture struct {
	TokenHash string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// [COMMENT]: Tạo fixture refresh token có thời hạn 7 ngày
func NewTestTokenFixture() TestTokenFixture {
	now := time.Now().UTC()
	return TestTokenFixture{
		TokenHash: "hash_of_" + TestRawRefreshToken,
		IssuedAt:  now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}
