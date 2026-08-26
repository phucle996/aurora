package fixtures

import (
	"github.com/google/uuid"
)

// [COMMENT]: Mock device IDs và Ed25519 fingerprints cho kiểm thử xác thực gắn thiết bị (Device-Bound Authentication)
var (
	TestDeviceID             = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	TestClientDeviceID       = "client-device-ed25519-abc123xyz"
	TestPublicKeyFingerprint = "fp_ed25519_sha256_mock_fingerprint_hash_value"
)

// TestDeviceFixture định nghĩa cấu trúc thông tin thiết bị mẫu
type TestDeviceFixture struct {
	ID                   uuid.UUID
	UserID               uuid.UUID
	DeviceName           string
	DeviceType           string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       string
}

// [COMMENT]: Khởi tạo fixture thiết bị chuẩn Chrome on Linux cho test
func NewTestDeviceFixture(userID uuid.UUID) TestDeviceFixture {
	return TestDeviceFixture{
		ID:                   TestDeviceID,
		UserID:               userID,
		DeviceName:           "Chrome on Linux",
		DeviceType:           "browser",
		PublicKey:            "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA...mock...\n-----END PUBLIC KEY-----",
		PublicKeyFingerprint: TestPublicKeyFingerprint,
		ClientDeviceID:       TestClientDeviceID,
	}
}
