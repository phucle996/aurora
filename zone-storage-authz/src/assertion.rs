use base64::Engine;
use moka::sync::Cache;
use serde::Deserialize;
use std::time::Duration;

use crate::canonical::sha256_hex;
use crate::error::AuthzError;
use crate::keys::AssertionKeys;

const MAX_ASSERTION_BYTES: usize = 8 * 1024;
const MAX_CLOCK_SKEW_SECONDS: i64 = 3;

#[derive(Clone, Debug, Deserialize)]
pub struct StorageAssertion {
    pub jti: String,
    pub access_session_id: String,
    pub binding_hash: String,
    pub actor_id: String,
    pub resource_id: String,
    pub bucket_name: String,
    pub workspace_id: String,
    pub zone_id: String,
    pub action: String,
    pub method: String,
    pub path_hash: String,
    pub body_hash: String,
    pub key_prefix: String,
    pub policy_revision: u64,
    pub issued_at: i64,
    pub expires_at: i64,
    pub audience: String,
    pub key_id: String,
}

#[derive(Clone)]
pub struct AssertionVerifier {
    zone_id: String,
    keys: AssertionKeys,
    replay: Cache<String, ()>,
}

impl AssertionVerifier {
    pub fn new(zone_id: String, keys: AssertionKeys, replay_capacity: u64) -> Self {
        Self {
            zone_id,
            keys,
            replay: Cache::builder()
                .max_capacity(replay_capacity)
                .time_to_live(Duration::from_secs(30))
                .build(),
        }
    }

    pub fn verify(
        &self,
        encoded: &str,
        signature: &str,
        key_id: &str,
        method: &str,
        path: &str,
        body: &[u8],
    ) -> Result<StorageAssertion, AuthzError> {
        if encoded.is_empty() || encoded.len() > MAX_ASSERTION_BYTES {
            return Err(AuthzError::Denied("ASSERTION_SIZE_INVALID"));
        }
        self.keys.verify(key_id, encoded.as_bytes(), signature)?;
        let decoded = base64::engine::general_purpose::URL_SAFE_NO_PAD
            .decode(encoded)
            .map_err(|_| AuthzError::Denied("ASSERTION_ENCODING_INVALID"))?;
        let assertion: StorageAssertion = serde_json::from_slice(&decoded)
            .map_err(|_| AuthzError::Denied("ASSERTION_JSON_INVALID"))?;
        let now = chrono_like_unix_seconds()?;
        if assertion.key_id != key_id
            || assertion.audience != "zone-storage-gateway"
            || assertion.zone_id != self.zone_id
            || assertion.method != method
            || assertion.path_hash != sha256_hex(path.as_bytes())
            || assertion.body_hash != sha256_hex(body)
            || assertion.issued_at > now.saturating_add(MAX_CLOCK_SKEW_SECONDS)
            || assertion.issued_at < now.saturating_sub(15)
            || assertion.expires_at < now.saturating_sub(MAX_CLOCK_SKEW_SECONDS)
            || assertion.expires_at > assertion.issued_at.saturating_add(15)
            || assertion.policy_revision == 0
            || uuid::Uuid::parse_str(&assertion.jti).is_err()
            || uuid::Uuid::parse_str(&assertion.access_session_id).is_err()
        {
            return Err(AuthzError::Denied("ASSERTION_BINDING_INVALID"));
        }
        // Moka's entry operation is atomic, so concurrent Envoy retries cannot
        // both consume the same assertion jti.
        if !self
            .replay
            .entry(assertion.jti.clone())
            .or_insert(())
            .is_fresh()
        {
            return Err(AuthzError::Denied("ASSERTION_REPLAYED"));
        }
        Ok(assertion)
    }
}

fn chrono_like_unix_seconds() -> Result<i64, AuthzError> {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_secs().min(i64::MAX as u64) as i64)
        .map_err(|_| AuthzError::Dependency("system clock is before Unix epoch".into()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};
    use std::collections::HashMap;

    #[test]
    fn valid_assertion_is_single_use() {
        let signing_key = SigningKey::from_bytes(&[7_u8; 32]);
        let key_id = "storage-assertion:v1";
        let zone_id = uuid::Uuid::new_v4().to_string();
        let session_id = uuid::Uuid::new_v4().to_string();
        let now = chrono_like_unix_seconds().unwrap();
        let path = "/storage/v1/buckets/ws-bucket/objects";
        let body = b"";
        let payload = serde_json::json!({
            "jti": uuid::Uuid::new_v4().to_string(),
            "access_session_id": session_id,
            "binding_hash": "a".repeat(64),
            "actor_id": uuid::Uuid::new_v4().to_string(),
            "resource_id": uuid::Uuid::new_v4().to_string(),
            "bucket_name": "ws-bucket",
            "workspace_id": uuid::Uuid::new_v4().to_string(),
            "zone_id": zone_id,
            "action": "ListBucket",
            "method": "GET",
            "path_hash": sha256_hex(path.as_bytes()),
            "body_hash": sha256_hex(body),
            "key_prefix": "",
            "policy_revision": 1,
            "issued_at": now,
            "expires_at": now + 5,
            "audience": "zone-storage-gateway",
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
}
