use std::collections::HashMap;
use std::sync::Arc;

use base64::Engine;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::config::Config;
use crate::token::TokenManager;
use crate::user::claims::Claims;

const ASSERTION_AUDIENCE: &str = "zone-control-edge-gateway";
const ASSERTION_ISSUER: &str = "aurora-acr";
const ASSERTION_CAPABILITY: &str = "storage.object";
const ASSERTION_TTL_SECONDS: i64 = 10;
const MAX_CONTROL_BODY_BYTES: usize = 64 * 1024;

#[derive(Deserialize)]
struct GenericTransferTicketRequest {
    capability: String,
    operation: String,
    access_session_id: String,
}

#[derive(Serialize)]
struct Assertion<'a> {
    schema_version: u32,
    jti: String,
    operation_id: String,
    access_session_id: &'a str,
    actor_id: &'a str,
    workspace_id: &'a str,
    zone_id: &'a str,
    capability: &'static str,
    action: &'a str,
    method: &'a str,
    path_hash: String,
    body_hash: String,
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

pub struct StorageControlWorkflowContext<'a> {
    pub token_mgr: &'a Arc<TokenManager>,
    pub config: &'a Config,
}

pub struct StorageControlRequest<'a> {
    pub claims: &'a Claims,
    pub workspace_id: &'a str,
    pub headers: &'a HashMap<String, String>,
    pub method: &'a str,
    pub path: &'a str,
    pub body: &'a [u8],
}

pub struct GenericTransferTicketRequestContext<'a> {
    pub claims: &'a Claims,
    pub workspace_id: &'a str,
    pub token_mgr: &'a Arc<TokenManager>,
    pub config: &'a Config,
    pub method: &'a str,
    pub path: &'a str,
    pub body: &'a [u8],
}

pub async fn attest_transfer_ticket_request(
    request: GenericTransferTicketRequestContext<'_>,
) -> Result<SignedControlHeaders, &'static str> {
    if request.body.len() > MAX_CONTROL_BODY_BYTES {
        return Err("Zone transfer ticket request body exceeds 64 KiB");
    }
    if request
        .config
        .vault
        .zone_control_assertion_key_path
        .is_empty()
        || request
            .config
            .vault
            .zone_control_assertion_key_id
            .is_empty()
    {
        return Err("Zone control assertion signer is not configured");
    }
    let envelope: GenericTransferTicketRequest = serde_json::from_slice(request.body)
        .map_err(|_| "Zone transfer ticket envelope is invalid")?;
    if envelope.capability.is_empty()
        || envelope.capability.len() > 96
        || !envelope
            .capability
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
        || !matches!(
            envelope.operation.as_str(),
            "upload"
                | "download"
                | "revoke"
                | "multipart_initiate"
                | "multipart_upload_part"
                | "multipart_complete"
                | "multipart_abort"
        )
        || uuid::Uuid::parse_str(&envelope.access_session_id).is_err()
        || uuid::Uuid::parse_str(&request.claims.uid).is_err()
        || uuid::Uuid::parse_str(request.workspace_id).is_err()
        || request
            .claims
            .zone_id
            .as_deref()
            .is_none_or(|zone_id| uuid::Uuid::parse_str(zone_id).is_err())
    {
        return Err("Zone transfer ticket envelope is invalid");
    }
    let now = chrono::Utc::now().timestamp();
    let assertion = Assertion {
        schema_version: 2,
        operation_id: uuid::Uuid::new_v4().to_string(),
        jti: uuid::Uuid::new_v4().to_string(),
        access_session_id: &envelope.access_session_id,
        actor_id: &request.claims.uid,
        workspace_id: request.workspace_id,
        zone_id: request.claims.zone_id.as_deref().unwrap_or_default(),
        capability: "zone.transfer.ticket",
        action: &envelope.operation,
        method: request.method,
        path_hash: hex_sha256(request.path.as_bytes()),
        body_hash: hex_sha256(request.body),
        issued_at: now,
        expires_at: now.saturating_add(ASSERTION_TTL_SECONDS),
        audience: ASSERTION_AUDIENCE,
        issuer: ASSERTION_ISSUER,
        key_id: &request.config.vault.zone_control_assertion_key_id,
    };
    let encoded_assertion = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(
        serde_json::to_vec(&assertion).map_err(|_| "Zone transfer assertion encoding failed")?,
    );
    let (actual_key_id, signature) = request
        .token_mgr
        .sign_zone_control_assertion(
            &request.config.vault.zone_control_assertion_key_path,
            encoded_assertion.as_bytes(),
        )
        .await
        .map_err(|_| "Zone control assertion signing failed")?;
    if actual_key_id != request.config.vault.zone_control_assertion_key_id {
        return Err("Zone control assertion signing key version mismatch");
    }
    Ok(SignedControlHeaders {
        access_session_id: envelope.access_session_id,
        assertion: encoded_assertion,
        signature,
        key_id: actual_key_id,
    })
}

pub async fn authorize_storage_and_sign(
    workflow: StorageControlWorkflowContext<'_>,
    request: StorageControlRequest<'_>,
) -> Result<SignedControlHeaders, &'static str> {
    let StorageControlWorkflowContext { token_mgr, config } = workflow;
    let StorageControlRequest {
        claims,
        workspace_id,
        headers,
        method,
        path,
        body,
    } = request;
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
    if uuid::Uuid::parse_str(&claims.uid).is_err()
        || uuid::Uuid::parse_str(workspace_id).is_err()
        || claims
            .zone_id
            .as_deref()
            .is_none_or(|zone_id| uuid::Uuid::parse_str(zone_id).is_err())
    {
        return Err("storage request context is incomplete");
    }
    let action = action_for_request(method, path).ok_or("storage route is not allowed")?;
    if !storage_body_is_allowed(action, body) {
        return Err("storage request body semantics are forbidden");
    }

    let now = chrono::Utc::now().timestamp();
    let jti = uuid::Uuid::new_v4().to_string();
    let assertion = Assertion {
        schema_version: 2,
        operation_id: uuid::Uuid::new_v4().to_string(),
        jti,
        access_session_id,
        actor_id: &claims.uid,
        workspace_id,
        zone_id: claims.zone_id.as_deref().unwrap_or_default(),
        capability: ASSERTION_CAPABILITY,
        action,
        method,
        path_hash: hex_sha256(path.as_bytes()),
        body_hash: hex_sha256(body),
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

fn hex_sha256(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}

#[cfg(test)]
#[path = "../../tests/unit/storage/control_assertion.rs"]
mod tests;
