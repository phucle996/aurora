package apperr

import (
	"errors"
	"testing"
)

func TestAppErrorUnwrapSupportsErrorsIs(t *testing.T) {
	kind := errors.New("domain kind")
	err := Wrap(kind, "reason_code", errors.New("raw cause"))
	if !errors.Is(err, kind) {
		t.Fatalf("expected errors.Is true")
	}

	appErr, ok := As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error extract success")
	}
	if appErr.Reason != "reason_code" {
		t.Fatalf("expected reason_code got %q", appErr.Reason)
	}
	if appErr.Cause == nil || appErr.Cause.Error() != "raw cause" {
		t.Fatalf("expected cause preserved")
	}
}

func TestAppErrorFormatsWithoutReason(t *testing.T) {
	kind := errors.New("iam: invalid")
	err := Wrap(kind, "", nil)
	if err.Error() != "iam: invalid" {
		t.Fatalf("expected kind-only error string, got %q", err.Error())
	}
}

func TestLogFieldsExtractsKindReasonCause(t *testing.T) {
	kind := errors.New("iam: admin login mfa invalid")
	err := Wrap(kind, "admin_login_mfa_validate_error", errors.New("line1\nline2"))

	fields := LogFields(err)
	if fields == nil {
		t.Fatalf("expected fields")
	}
	if fields["error_kind"] != kind.Error() {
		t.Fatalf("unexpected error_kind: %v", fields["error_kind"])
	}
	if fields["error_reason"] != "admin_login_mfa_validate_error" {
		t.Fatalf("unexpected error_reason: %v", fields["error_reason"])
	}
	if fields["error_cause"] != "line1 line2" {
		t.Fatalf("unexpected error_cause: %v", fields["error_cause"])
	}
}

func TestLogFieldsRedactsSensitiveCause(t *testing.T) {
	kind := errors.New("iam: admin login mfa invalid")
	err := Wrap(kind, "admin_login_mfa_validate_error", errors.New("secret=abc token=xyz"))

	fields := LogFields(err)
	if fields == nil {
		t.Fatalf("expected fields")
	}
	if fields["error_cause"] != "[redacted_sensitive_cause]" {
		t.Fatalf("expected redacted cause, got %v", fields["error_cause"])
	}
}

func TestLogFieldsIgnoreNonEnvelope(t *testing.T) {
	fields := LogFields(errors.New("boom"))
	if fields != nil {
		t.Fatalf("expected nil fields")
	}
}
