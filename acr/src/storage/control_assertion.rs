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

const ASSERTION_AUDIENCE: &str = "zone-control-edge-gateway";
const ASSERTION_ISSUER: &str = "aurora-acr";
const ASSERTION_CAPABILITY: &str = "storage.object";
const ASSERTION_TTL_SECONDS: i64 = 10;
const MAX_CONTROL_BODY_BYTES: usize = 64 * 1024;

#[derive(Serialize)]
struct Assertion<'a> {
    schema_version: u32,
    jti: String,
    operation_id: String,
    access_session_id: &'a str,
    binding_hash: &'a str,
    actor_id: &'a str,
    resource_id: &'a str,
    resource_name: &'a str,
    workspace_id: &'a str,
    zone_id: &'a str,
    capability: &'static str,
    action: &'a str,
    method: &'a str,
    path_hash: String,
    body_hash: String,
    scope: &'a str,
    policy_revision: u64,
    issued_at: i64,
    expires_at: i64,
    audience: &'static str,
    issuer: &'static str,
    key_id: &'a str,
}

pub struct SignedControlHeaders {
    pub access_session_id: String,
    pub assertion: String,
    pub signature: String,
    pub key_id: String,
}

// Keep each signed assertion input explicit at this security boundary. A generic
// context would make authority and canonicalization inputs easier to mix up.
#[allow(clippy::too_many_arguments)]
pub async fn authorize_storage_and_sign(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    claims: &Claims,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
    body: &[u8],
) -> Result<SignedControlHeaders, &'static str> {
    if body.len() > MAX_CONTROL_BODY_BYTES {
        return Err("Zone control request body exceeds 64 KiB");
    }
    if config.vault.zone_control_assertion_key_path.is_empty()
        || config.vault.zone_control_assertion_key_id.is_empty()
    {
        return Err("Zone control assertion signer is not configured");
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
    if !storage_body_is_allowed(action, body) {
        return Err("storage request body semantics are forbidden");
    }
    let allowed: HashSet<&str> = record.actions.iter().map(String::as_str).collect();
    if !allowed.contains(action)
        || !path_targets_bucket(path, &record.bucket_name, &record.key_prefix)
    {
        return Err("storage request exceeds access scope");
    }

    let jti = uuid::Uuid::new_v4().to_string();
    let assertion = Assertion {
        schema_version: 1,
        operation_id: uuid::Uuid::new_v4().to_string(),
        jti,
        access_session_id,
        binding_hash: &record.binding_hash,
        actor_id: &record.actor_id,
        resource_id: &record.resource_id,
        resource_name: &record.bucket_name,
        workspace_id: &record.workspace_id,
        zone_id: &record.zone_id,
        capability: ASSERTION_CAPABILITY,
        action,
        method,
        path_hash: hex_sha256(path.as_bytes()),
        body_hash: hex_sha256(body),
        scope: &record.key_prefix,
        policy_revision: record.policy_revision,
        issued_at: now,
        expires_at: now.saturating_add(ASSERTION_TTL_SECONDS),
        audience: ASSERTION_AUDIENCE,
        issuer: ASSERTION_ISSUER,
        key_id: &config.vault.zone_control_assertion_key_id,
    };
    let json =
        serde_json::to_vec(&assertion).map_err(|_| "Zone control assertion encoding failed")?;
    let encoded_assertion = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(json);
    let (actual_key_id, signature) = token_mgr
        .sign_zone_control_assertion(
            &config.vault.zone_control_assertion_key_path,
            encoded_assertion.as_bytes(),
        )
        .await
        .map_err(|_| "Zone control assertion signing failed")?;
    if actual_key_id != config.vault.zone_control_assertion_key_id {
        return Err("Zone control assertion signing key version mismatch");
    }
    Ok(SignedControlHeaders {
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
    let resource_route = path_only
        .strip_prefix("/zone-control/v1/storage/buckets/")?
        .split_once('/')?;
    if resource_route.0.is_empty() {
        return None;
    }
    let route = resource_route.1;
    if route
        .strip_prefix("objects/")
        .and_then(|object| object.strip_suffix("/tags"))
        .is_some_and(|object| !object.is_empty())
    {
        return match method {
            "GET" => Some("GetObjectTagging"),
            "PUT" => Some("PutObjectTagging"),
            _ => None,
        };
    }
    if route == "bulk-delete" && method == "POST" {
        return Some("DeleteObject");
    }
    if route == "presign-upload" && method == "POST" {
        return Some("PutObject");
    }
    if route == "presign-download" && method == "POST" {
        return Some("GetObject");
    }
    if method == "GET" && route == "objects" {
        return Some("ListBucket");
    }
    if method == "HEAD"
        && route
            .strip_prefix("objects/")
            .is_some_and(|object| !object.is_empty())
    {
        return Some("GetObject");
    }
    None
}

fn path_targets_bucket(path: &str, bucket_name: &str, key_prefix: &str) -> bool {
    let path_only = path.split('?').next().unwrap_or(path);
    let has_query = path.contains('?');
    let lowered = path_only.to_ascii_lowercase();
    // Envoy and S3 must see the same canonical path. Reject encoded slash/dot
    // segments and traversal-looking separators instead of guessing which
    // decoder will run first.
    if lowered.contains("%2f")
        || lowered.contains("%5c")
        || lowered.contains("%2e")
        || lowered.contains("%25")
        || path_only.contains('\\')
        || path_only.contains("//")
        || path_only
            .split('/')
            .any(|segment| segment == "." || segment == "..")
    {
        return false;
    }
    let expected = format!("/zone-control/v1/storage/buckets/{bucket_name}");
    if !path_only.starts_with(&expected) {
        return false;
    }
    if path_only.len() > expected.len() && path_only.as_bytes()[expected.len()] != b'/' {
        return false;
    }
    // Only ListObjectsV2 has an explicit query contract. Object versions and
    // every other S3 subresource require a distinct capability.
    if has_query && !path_only.ends_with("/objects") {
        return false;
    }
    if key_prefix.is_empty() {
        return !path_only.ends_with("/objects") || list_query_is_allowed(path, key_prefix);
    }
    if let Some((_, key)) = path_only.split_once("/objects/") {
        return key.starts_with(key_prefix);
    }
    path_only.ends_with("/objects") && list_query_is_allowed(path, key_prefix)
}

fn contains_xml_element(body: &[u8], forbidden_local_name: &str) -> bool {
    let Ok(xml) = std::str::from_utf8(body) else {
        return true;
    };
    let lowercase = xml.to_ascii_lowercase();
    let mut remaining = lowercase.as_str();
    while let Some(start) = remaining.find('<') {
        remaining = &remaining[start + 1..];
        let Some(end) = remaining.find('>') else {
            return false;
        };
        let token = remaining[..end].trim_start_matches('/').trim();
        let qualified_name = token
            .split_ascii_whitespace()
            .next()
            .unwrap_or("")
            .trim_end_matches('/');
        if qualified_name
            .rsplit(':')
            .next()
            .is_some_and(|name| name == forbidden_local_name)
        {
            return true;
        }
        remaining = &remaining[end + 1..];
    }
    false
}

fn storage_body_is_allowed(action: &str, body: &[u8]) -> bool {
    match action {
        "ListBucket" | "GetObject" | "GetObjectTagging" => body.is_empty(),
        // Permanent deletion of a specific object version requires a separate
        // capability; DeleteObject must not inherit it through XML.
        "DeleteObject" => !body.is_empty() && !contains_xml_element(body, "versionid"),
        "PutObjectTagging" => !body.is_empty() && std::str::from_utf8(body).is_ok(),
        // Presign request schemas are bounded and signed but are not S3 bytes.
        "PutObject" => true,
        _ => false,
    }
}

fn list_query_is_allowed(path: &str, key_prefix: &str) -> bool {
    let Some((_, query)) = path.split_once('?') else {
        return false;
    };
    if !has_valid_percent_encoding(query) {
        return false;
    }
    let mut list_type = false;
    let mut prefix = false;
    let mut delimiter = false;
    let mut max_keys = false;
    let mut continuation = false;
    let mut start_after = false;
    let mut encoding = false;
    let mut fetch_owner = false;
    for (name, value) in url::form_urlencoded::parse(query.as_bytes()) {
        // A lossy decode cannot be used at an authorization boundary because
        // MinIO may canonicalize the original bytes differently.
        if name.contains('\u{fffd}') || value.contains('\u{fffd}') {
            return false;
        }
        match name.as_ref() {
            "list-type" if !list_type && value == "2" => list_type = true,
            "prefix"
                if !prefix
                    && value.len() <= 1_024
                    && (key_prefix.is_empty() || value.starts_with(key_prefix)) =>
            {
                prefix = true
            }
            "delimiter" if !delimiter && value.len() <= 1_024 => delimiter = true,
            "max-keys"
                if !max_keys
                    && value
                        .parse::<u16>()
                        .is_ok_and(|count| (1..=1_000).contains(&count)) =>
            {
                max_keys = true
            }
            "continuation-token" if !continuation && !value.is_empty() && value.len() <= 4_096 => {
                continuation = true
            }
            "start-after"
                if !start_after
                    && value.len() <= 1_024
                    && (key_prefix.is_empty() || value.starts_with(key_prefix)) =>
            {
                start_after = true
            }
            "encoding-type" if !encoding && value == "url" => encoding = true,
            "fetch-owner" if !fetch_owner && value == "false" => fetch_owner = true,
            _ => return false,
        }
    }
    list_type && (key_prefix.is_empty() || prefix) && !(continuation && start_after)
}

fn has_valid_percent_encoding(value: &str) -> bool {
    let bytes = value.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            if index + 2 >= bytes.len()
                || !bytes[index + 1].is_ascii_hexdigit()
                || !bytes[index + 2].is_ascii_hexdigit()
            {
                return false;
            }
            index += 3;
        } else {
            index += 1;
        }
    }
    true
}

fn hex_sha256(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}

#[cfg(test)]
mod tests {
    use super::{
        action_for_request, contains_xml_element, path_targets_bucket, storage_body_is_allowed,
    };

    #[test]
    fn storage_control_route_is_classified_and_bound() {
        let list = "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2";
        assert_eq!(action_for_request("GET", list), Some("ListBucket"));
        assert_eq!(
            action_for_request(
                "GET",
                "/zone-control/v1/storage/buckets/ws-bucket/unreviewed/objects?list-type=2"
            ),
            None
        );
        assert!(path_targets_bucket(list, "ws-bucket", ""));
        assert!(!path_targets_bucket(list, "ws-bucket", "logs/"));
        assert!(path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2&prefix=logs%2F",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?prefix=logs%2F&prefix=",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?acl&list-type=2",
            "ws-bucket",
            ""
        ));
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=1",
            "ws-bucket",
            ""
        ));
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects/a?versionId=1",
            "ws-bucket",
            ""
        ));

        let object = "/zone-control/v1/storage/buckets/ws-bucket/objects/logs/2026/a.json";
        assert_eq!(action_for_request("HEAD", object), Some("GetObject"));
        assert!(path_targets_bucket(object, "ws-bucket", "logs/"));
        assert!(!path_targets_bucket(object, "other-bucket", "logs/"));
    }

    #[test]
    fn ambiguous_storage_path_is_rejected() {
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket/objects/%2e%2e/private",
            "ws-bucket",
            ""
        ));
        assert!(!path_targets_bucket(
            "/zone-control/v1/storage/buckets/ws-bucket//objects/a",
            "ws-bucket",
            ""
        ));
    }

    #[test]
    fn bulk_delete_does_not_inherit_version_delete() {
        assert!(contains_xml_element(
            b"<Delete><Object><Key>a</Key><VersionId>v1</VersionId></Object></Delete>",
            "versionid"
        ));
        assert!(!contains_xml_element(
            b"<Delete><Object><Key>versionid.txt</Key></Object></Delete>",
            "versionid"
        ));
        assert!(!storage_body_is_allowed("ListBucket", b"unexpected"));
        assert!(!storage_body_is_allowed("PutObjectTagging", b""));
    }
}
