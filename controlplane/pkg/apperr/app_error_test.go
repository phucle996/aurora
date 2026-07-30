package apperr

import "testing"

func TestSanitizeLogTextRedactsCredentialURI(t *testing.T) {
	value := "dial postgres://app_user:plain-text-password@postgres:5432/controlplane failed"
	if got := SanitizeLogText(value); got != "[redacted_sensitive_cause]" {
		t.Fatalf("SanitizeLogText() = %q", got)
	}
}
