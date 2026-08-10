use super::{critical_message, login_message, verifying_key};

#[test]
fn rejects_empty_public_key() {
    assert!(verifying_key("").is_err());
}

#[test]
fn canonical_messages_have_stable_field_order() {
    assert_eq!(
        login_message("c", "n", "alice", "acme", "vn", true, 7),
        "aurora.login-proof.v1\nc\nn\nalice\nacme\nvn\ntrue\n7"
    );
    assert_eq!(
        critical_message("c", "n", "post", "/api/v1/critical/x", "abc", 7),
        "aurora.session-proof.v1\nc\nn\nPOST\n/api/v1/critical/x\nabc\n7"
    );
}
