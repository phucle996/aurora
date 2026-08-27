use super::*;
use ed25519_dalek::{Signer, SigningKey};
use std::collections::HashMap;

#[test]
fn valid_assertion_is_single_use() {
    let signing_key = SigningKey::from_bytes(&[7_u8; 32]);
    let key_id = "zone-control-assertion:v1";
    let zone_id = uuid::Uuid::new_v4().to_string();
    let session_id = uuid::Uuid::new_v4().to_string();
    let now = chrono_like_unix_seconds().unwrap();
    let path = "/zone-control/v1/storage/buckets/ws-bucket/objects";
    let body = b"";
    let payload = serde_json::json!({
        "schema_version": 2,
        "jti": uuid::Uuid::new_v4().to_string(),
        "operation_id": uuid::Uuid::new_v4().to_string(),
        "access_session_id": session_id,
        "actor_id": uuid::Uuid::new_v4().to_string(),
        "workspace_id": uuid::Uuid::new_v4().to_string(),
        "zone_id": zone_id,
        "capability": "storage.object",
        "action": "ListBucket",
        "method": "GET",
        "path_hash": sha256_hex(path.as_bytes()),
        "body_hash": sha256_hex(body),
        "issued_at": now,
        "expires_at": now + 5,
        "audience": "zone-control-edge-gateway",
        "issuer": "aurora-acr",
        "key_id": key_id,
    });
    let encoded = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(serde_json::to_vec(&payload).unwrap());
    let signature = base64::engine::general_purpose::STANDARD
        .encode(signing_key.sign(encoded.as_bytes()).to_bytes());
    let mut keys = HashMap::new();
    keys.insert(key_id.to_string(), signing_key.verifying_key().to_bytes());
    let verifier = AssertionVerifier::new(
        payload["zone_id"].as_str().unwrap().to_string(),
        AssertionKeys::new(keys).unwrap(),
        100,
    );
    assert!(verifier
        .verify(&encoded, &signature, key_id, "GET", path, body)
        .is_ok());
    assert!(verifier
        .verify(&encoded, &signature, key_id, "GET", path, body)
        .is_err());
}
