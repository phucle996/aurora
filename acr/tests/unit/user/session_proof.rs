use super::{canonicalize_public_key, critical_message, login_message, verify, verifying_key};
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use ed25519_dalek::{Signer, SigningKey};

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

#[test]
fn recovered_durable_public_key_still_requires_the_matching_private_key() {
    let durable_signer = SigningKey::from_bytes(&[7; 32]);
    let other_signer = SigningKey::from_bytes(&[8; 32]);
    let public_key = BASE64.encode(durable_signer.verifying_key().as_bytes());
    let canonical = canonicalize_public_key(&format!("  {public_key}  ")).unwrap();
    let message = "aurora.session-proof.recovery-test";

    let valid_signature = BASE64.encode(durable_signer.sign(message.as_bytes()).to_bytes());
    assert!(verify(&canonical, message, &valid_signature).is_ok());

    let stolen_refresh_signature = BASE64.encode(other_signer.sign(message.as_bytes()).to_bytes());
    assert!(verify(&canonical, message, &stolen_refresh_signature).is_err());
}
