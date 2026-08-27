use base64::Engine;
use moka::sync::Cache;
use serde::Deserialize;
use std::time::Duration;

use crate::error::AuthzError;
use crate::keys::AssertionKeys;
use crate::request_binding::sha256_hex;

const MAX_ASSERTION_BYTES: usize = 8 * 1024;
const MAX_CLOCK_SKEW_SECONDS: i64 = 3;
const ASSERTION_AUDIENCE: &str = "zone-control-edge-gateway";
const ASSERTION_ISSUER: &str = "aurora-acr";
const ASSERTION_SCHEMA_VERSION: u32 = 2;

#[derive(Clone, Debug, Deserialize)]
pub struct ControlAssertion {
    pub schema_version: u32,
    pub jti: String,
    pub operation_id: String,
    pub access_session_id: String,
    pub actor_id: String,
    pub workspace_id: String,
    pub zone_id: String,
    pub capability: String,
    pub action: String,
    pub method: String,
    pub path_hash: String,
    pub body_hash: String,
    pub issued_at: i64,
    pub expires_at: i64,
    pub audience: String,
    pub issuer: String,
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
    ) -> Result<ControlAssertion, AuthzError> {
        if encoded.is_empty() || encoded.len() > MAX_ASSERTION_BYTES {
            return Err(AuthzError::Denied("ASSERTION_SIZE_INVALID"));
        }
        self.keys.verify(key_id, encoded.as_bytes(), signature)?;
        let decoded = base64::engine::general_purpose::URL_SAFE_NO_PAD
            .decode(encoded)
            .map_err(|_| AuthzError::Denied("ASSERTION_ENCODING_INVALID"))?;
        let assertion: ControlAssertion = serde_json::from_slice(&decoded)
            .map_err(|_| AuthzError::Denied("ASSERTION_JSON_INVALID"))?;
        let now = chrono_like_unix_seconds()?;
        if assertion.schema_version != ASSERTION_SCHEMA_VERSION
            || assertion.key_id != key_id
            || assertion.audience != ASSERTION_AUDIENCE
            || assertion.issuer != ASSERTION_ISSUER
            || assertion.zone_id != self.zone_id
            || !matches!(
                assertion.capability.as_str(),
                "storage.object" | "zone.transfer.ticket"
            )
            || assertion.method != method
            || assertion.path_hash != sha256_hex(path.as_bytes())
            || assertion.body_hash != sha256_hex(body)
            || assertion.issued_at > now.saturating_add(MAX_CLOCK_SKEW_SECONDS)
            || assertion.issued_at < now.saturating_sub(15)
            || assertion.expires_at < now.saturating_sub(MAX_CLOCK_SKEW_SECONDS)
            || assertion.expires_at > assertion.issued_at.saturating_add(15)
            || uuid::Uuid::parse_str(&assertion.jti).is_err()
            || uuid::Uuid::parse_str(&assertion.operation_id).is_err()
            || uuid::Uuid::parse_str(&assertion.access_session_id).is_err()
            || uuid::Uuid::parse_str(&assertion.actor_id).is_err()
            || uuid::Uuid::parse_str(&assertion.workspace_id).is_err()
        {
            return Err(AuthzError::Denied("ASSERTION_BINDING_INVALID"));
        }
        // This cache is an in-process replay shield, not a distributed
        // exactly-once claim. Mutating capabilities must still carry the
        // operation_id into an idempotent downstream boundary.
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
#[path = "../tests/unit/control_assertion.rs"]
mod tests;
