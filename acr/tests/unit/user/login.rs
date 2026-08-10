use super::canonicalize_login_identity;

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
