use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use base64::Engine;
use prost::Message;
use redis::AsyncCommands;
use serde::Serialize;
use sha2::{Digest, Sha256};

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::token::TokenManager;
use crate::user::claims::Claims;

const ASSERTION_AUDIENCE: &str = "zone-storage-gateway";
const ASSERTION_TTL_SECONDS: i64 = 10;

#[derive(Serialize)]
struct Assertion<'a> {
    jti: String,
    access_session_id: &'a str,
    binding_hash: &'a str,
    actor_id: &'a str,
    resource_id: &'a str,
    bucket_name: &'a str,
    workspace_id: &'a str,
    zone_id: &'a str,
    action: &'a str,
    method: &'a str,
    path_hash: String,
    body_hash: String,
    key_prefix: &'a str,
    policy_revision: u64,
    issued_at: i64,
    expires_at: i64,
    audience: &'static str,
    key_id: &'a str,
}

pub struct SignedHeaders {
    pub access_session_id: String,
    pub assertion: String,
    pub signature: String,
    pub key_id: String,
}

pub async fn authorize_and_sign(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    claims: &Claims,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
    body: &[u8],
) -> Result<SignedHeaders, &'static str> {
    if config.vault.storage_assertion_key_path.is_empty()
        || config.vault.storage_assertion_key_id.is_empty()
    {
        return Err("storage assertion signer is not configured");
    }
    let access_session_id = headers
        .get("x-aurora-access-session-id")
        .or_else(|| headers.get("X-Aurora-Access-Session-Id"))
        .filter(|value| uuid::Uuid::parse_str(value).is_ok())
        .ok_or("storage access session is missing")?;
    let mut connection = session_mgr
        .get_connection()
        .await
        .map_err(|_| "auth-state redis unavailable")?;
    let encoded: Option<Vec<u8>> = connection
        .get(format!("storage_access:{{{access_session_id}}}"))
        .await
        .map_err(|_| "auth-state redis read failed")?;
    let record = crate::storage::access_record_proto::StorageAccessRecord::decode(
        encoded
            .as_deref()
            .ok_or("storage access session not found")?,
    )
    .map_err(|_| "storage access session is corrupt")?;
    let now = chrono::Utc::now().timestamp();
    if record.schema_version != 1
        || record.access_session_id != *access_session_id
        || record.actor_id != claims.uid
        || record.zone_id != claims.zone_id.as_deref().unwrap_or_default()
        || record.expires_at_unix_seconds <= now as u64
        || record.policy_revision == 0
    {
        return Err("storage access session binding mismatch");
    }
    let action = action_for_request(method, path).ok_or("storage route is not allowed")?;
    let allowed: HashSet<&str> = record.actions.iter().map(String::as_str).collect();
    if !allowed.contains(action)
        || !path_targets_bucket(path, &record.bucket_name, &record.key_prefix)
    {
        return Err("storage request exceeds access scope");
    }

    let assertion = Assertion {
        jti: uuid::Uuid::new_v4().to_string(),
        access_session_id,
        binding_hash: &record.binding_hash,
        actor_id: &record.actor_id,
        resource_id: &record.resource_id,
        bucket_name: &record.bucket_name,
        workspace_id: &record.workspace_id,
        zone_id: &record.zone_id,
        action,
        method,
        path_hash: hex_sha256(path.as_bytes()),
        body_hash: hex_sha256(body),
        key_prefix: &record.key_prefix,
        policy_revision: record.policy_revision,
        issued_at: now,
        expires_at: now.saturating_add(ASSERTION_TTL_SECONDS),
        audience: ASSERTION_AUDIENCE,
        key_id: &config.vault.storage_assertion_key_id,
    };
    let json = serde_json::to_vec(&assertion).map_err(|_| "storage assertion encoding failed")?;
    let encoded_assertion = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(json);
    let (actual_key_id, signature) = token_mgr
        .sign_storage_assertion(
            &config.vault.storage_assertion_key_path,
            encoded_assertion.as_bytes(),
        )
        .await
        .map_err(|_| "storage assertion signing failed")?;
    if actual_key_id != config.vault.storage_assertion_key_id {
        return Err("storage assertion signing key version mismatch");
    }
    Ok(SignedHeaders {
        access_session_id: access_session_id.to_string(),
        assertion: encoded_assertion,
        signature,
        key_id: actual_key_id,
    })
}

fn action_for_request(method: &str, path: &str) -> Option<&'static str> {
    // Query parameters are part of the signed canonical path, but route/action
    // classification must use the path component so ListObjectsV2's
    // `list-type=2` does not become an unknown action and fail closed.
    let path_only = path.split('?').next().unwrap_or(path);
    if path_only.ends_with("/tags") {
        return match method {
            "GET" => Some("GetObjectTagging"),
            "PUT" => Some("PutObjectTagging"),
            _ => None,
        };
    }
    if path_only.ends_with("/bulk-delete") && method == "POST" {
        return Some("DeleteObject");
    }
    if path_only.ends_with("/presign-upload") && method == "POST" {
        return Some("PutObject");
    }
    if path_only.ends_with("/presign-download") && method == "POST" {
        return Some("GetObject");
    }
    if method == "GET" && path_only.ends_with("/objects") {
        return Some("ListBucket");
    }
    if method == "HEAD" && path.contains("/objects/") {
        return Some("GetObject");
    }
    None
}

fn path_targets_bucket(path: &str, bucket_name: &str, key_prefix: &str) -> bool {
    let path_only = path.split('?').next().unwrap_or(path);
    let lowered = path_only.to_ascii_lowercase();
    // Envoy and S3 must see the same canonical path. Reject encoded slash/dot
    // segments and traversal-looking separators instead of guessing which
    // decoder will run first.
    if lowered.contains("%2f")
        || lowered.contains("%5c")
        || lowered.contains("%2e")
        || path_only.contains("//")
        || path_only.split('/').any(|segment| segment == "..")
    {
        return false;
    }
    let expected = format!("/storage/v1/buckets/{bucket_name}");
    if !path_only.starts_with(&expected) {
        return false;
    }
    if path_only.len() > expected.len() && path_only.as_bytes()[expected.len()] != b'/' {
        return false;
    }
    if key_prefix.is_empty() {
        return true;
    }
    path_only
        .split_once("/objects/")
        .map(|(_, key)| key.starts_with(key_prefix))
        .unwrap_or(path_only.ends_with("/objects"))
}

fn hex_sha256(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}
