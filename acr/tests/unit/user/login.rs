use super::canonicalize_login_identity;

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
