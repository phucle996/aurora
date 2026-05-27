package apperr

import (
	"errors"
	"testing"
)

func TestAppErrorUnwrapSupportsErrorsIs(t *testing.T) {
	kind := errors.New("domain kind")
	err := Wrap(kind, errors.New("raw cause"), "reason_code")
	if !errors.Is(err, kind) {
		t.Fatalf("expected errors.Is true")
	}

	appErr, ok := As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error extract success")
	}
	if appErr.Outcome != "reason_code" {
		t.Fatalf("expected reason_code got %q", appErr.Outcome)
	}
	if appErr.Cause == nil || appErr.Cause.Error() != "raw cause" {
		t.Fatalf("expected cause preserved")
	}
}

func TestAppErrorFormatsWithoutReason(t *testing.T) {
	kind := errors.New("iam: invalid")
	err := Wrap(kind, nil, "")
	if err.Error() != "iam: invalid" {
		t.Fatalf("expected kind-only error string, got %q", err.Error())
	}
}

func TestLogFieldsExtractsKindReasonCause(t *testing.T) {
	kind := errors.New("iam: admin login mfa invalid")
	err := Wrap(kind, errors.New("line1\nline2"), "admin_login_mfa_validate_error")

	fields := LogFields(err)
	if fields == nil {
		t.Fatalf("expected fields")
	}
	// error_kind bị loại bỏ khỏi LogFields — đã có trong `error` field của logger rồi.
	if _, exists := fields["error_kind"]; exists {
		t.Fatalf("error_kind should not be present in LogFields output, got: %v", fields["error_kind"])
	}
	if fields["outcome"] != "admin_login_mfa_validate_error" {
		t.Fatalf("unexpected outcome: %v", fields["outcome"])
	}
	if fields["error_cause"] != "line1 line2" {
		t.Fatalf("unexpected error_cause: %v", fields["error_cause"])
	}
}

func TestLogFieldsRedactsSensitiveCause(t *testing.T) {
	kind := errors.New("iam: admin login mfa invalid")
	err := Wrap(kind, errors.New("secret=abc token=xyz"), "admin_login_mfa_validate_error")

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
