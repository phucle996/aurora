package fixtures

import (
	"testing"
	"time"
)

func TestIAMFixturesAreStableAndBounded(t *testing.T) {
	user := NewTestUserFixture()
	if user.ID != TestPlatformUserID || user.Username == "" || user.Email == "" || user.PasswordHash == "" || user.Status != "active" {
		t.Fatalf("invalid user fixture: %#v", user)
	}

	device := NewTestDeviceFixture(user.ID)
	if device.UserID != user.ID || device.ID != TestDeviceID || device.ClientDeviceID == "" ||
		device.PublicKey == "" || device.PublicKeyFingerprint == "" {
		t.Fatalf("invalid device fixture: %#v", device)
	}

	token := NewTestTokenFixture()
	if token.TokenHash == "" || token.IssuedAt.IsZero() || !token.ExpiresAt.After(token.IssuedAt) ||
		token.ExpiresAt.Sub(token.IssuedAt) != 7*24*time.Hour {
		t.Fatalf("invalid token fixture: %#v", token)
	}
}
