use super::{canonicalize_login_identity, public_login_rejection};

#[test]
fn user_session_error_keeps_invalid_zone_separate_from_internal_failures() {
    use crate::error::AcrError;
    assert!(std::mem::size_of::<AcrError>() <= 32);
    let invalid: tonic::Status =
        AcrError::InvalidArgument("User session requires a concrete zone".into()).into();
    assert_eq!(invalid.code(), tonic::Code::InvalidArgument);
    assert_eq!(invalid.message(), "User session requires a concrete zone");
    let internal: tonic::Status = AcrError::Internal("private infrastructure detail".into()).into();
    assert_eq!(internal.code(), tonic::Code::Internal);
    assert_eq!(internal.message(), "Internal server error");
}

#[test]
fn canonical_tenant_login_uses_separate_fields() {
    let identity = canonicalize_login_identity(Some(" Alice "), Some(" ACME.Example "));
    assert_eq!(
        identity,
        Ok(("alice".to_string(), "acme.example".to_string()))
    );
}

#[test]
fn legacy_combined_username_is_rejected() {
    assert_eq!(
        canonicalize_login_identity(Some("alice@acme.example"), None),
        Err("Username must not contain tenant domain")
    );
}

#[test]
fn login_rejection_keeps_account_state_internal_but_marks_outages_as_retryable() {
    for reason in [
        "INVALID_CREDENTIALS",
        "ACCOUNT_DISABLED",
        "ACCOUNT_SUSPENDED",
        "ACCOUNT_NOT_READY",
        "unknown_reason",
    ] {
        let (status, message) = public_login_rejection(reason);
        assert_eq!(
            status as i32,
            envoy_types::ext_authz::v3::pb::HttpStatusCode::Unauthorized as i32
        );
        assert_eq!(
            message,
            "We couldn't sign you in. Check your details and try again."
        );
    }
    let (status, message) = public_login_rejection("AUTHENTICATION_UNAVAILABLE");
    assert_eq!(
        status as i32,
        envoy_types::ext_authz::v3::pb::HttpStatusCode::InternalServerError as i32
    );
    assert_eq!(
        message,
        "Authentication is temporarily unavailable. Please try again."
    );
}
