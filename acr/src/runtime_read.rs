use std::{sync::Arc, time::Duration};

use base64::Engine;
use prost::Message;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::config::Config;
use crate::infra::shared_redis::SharedRedisBus;
use crate::token::TokenManager;
use crate::user::claims::Claims;

const ASSERTION_AUDIENCE: &str = "zone-public-edge-gateway";
const ASSERTION_ISSUER: &str = "aurora-acr";
const ASSERTION_TTL_SECONDS: i64 = 10;
const AUTHORIZATION_REQUEST_CHANNEL: &str = "iam.authorization.runtime.get";
const AUTHORIZATION_REPLY_PREFIX: &str = "iam.authorization.runtime.reply.";

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MintRuntimeReadRequest {
    resource_type: String,
    resource_id: String,
    panel: String,
    #[serde(default)]
    component_id: Option<String>,
    from_seconds: u64,
}

#[derive(Serialize)]
struct RuntimeReadAssertion<'a> {
    schema_version: u32,
    jti: String,
    actor_id: &'a str,
    owner_id: &'a str,
    owner_type: &'static str,
    workspace_id: &'a str,
    zone_id: &'a str,
    module: &'static str,
    resource_type: &'static str,
    resource_id: String,
    panel_id: String,
    component_id: Option<String>,
    capability: &'static str,
    method: &'static str,
    path_hash: String,
    issued_at: i64,
    expires_at: i64,
    audience: &'static str,
    issuer: &'static str,
    key_id: &'a str,
}

#[derive(Serialize)]
pub struct RuntimeReadTicket {
    pub assertion: String,
    pub signature: String,
    pub key_id: String,
    pub zone_id: String,
    pub zone_code: String,
    pub method: &'static str,
    pub path: String,
    pub expires_at: String,
}

pub async fn mint(
    token_mgr: &Arc<TokenManager>,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    claims: &Claims,
    workspace_id: &str,
    zone_code: &str,
    raw_body: &[u8],
) -> Result<RuntimeReadTicket, &'static str> {
    if config.vault.zone_control_assertion_key_path.is_empty()
        || config.vault.zone_control_assertion_key_id.is_empty()
        || raw_body.is_empty()
        || raw_body.len() > 16 * 1024
    {
        return Err("runtime assertion request is invalid");
    }
    let actor_user_id = uuid::Uuid::parse_str(&claims.uid)
        .ok()
        .filter(|value| !value.is_nil())
        .ok_or("runtime assertion actor is invalid")?;
    let workspace_id = uuid::Uuid::parse_str(workspace_id)
        .ok()
        .filter(|value| !value.is_nil())
        .ok_or("runtime assertion workspace is invalid")?;
    let zone_id = claims
        .zone_id
        .as_deref()
        .and_then(|value| uuid::Uuid::parse_str(value).ok())
        .filter(|value| !value.is_nil())
        .ok_or("runtime assertion Zone is invalid")?;
    if !zone_code_token(zone_code) {
        return Err("runtime assertion Zone code is invalid");
    }

    let request: MintRuntimeReadRequest =
        serde_json::from_slice(raw_body).map_err(|_| "runtime assertion body is invalid")?;
    let resource_id = uuid::Uuid::parse_str(&request.resource_id)
        .ok()
        .filter(|value| !value.is_nil() && value.to_string() == request.resource_id)
        .ok_or("runtime assertion resource is invalid")?;
    let panel = match request.panel.as_str() {
        "health" | "metrics" | "logs" | "events" => request.panel,
        _ => return Err("runtime assertion panel is invalid"),
    };
    if !(1..=300).contains(&request.from_seconds) {
        return Err("runtime assertion window is invalid");
    }
    let component_id = request
        .component_id
        .map(|value| {
            if runtime_component_token(&value) {
                Ok(value)
            } else {
                Err("runtime assertion component is invalid")
            }
        })
        .transpose()?;

    // The public resource type is an ACR-owned registry. A client cannot pick
    // a permission or an internal adapter path independently.
    let (module, internal_resource_type, permission) =
        runtime_resource_contract(&request.resource_type)
            .ok_or("runtime assertion resource type is not enabled")?;
    if module == "storage" && (panel != "metrics" || component_id.is_some()) {
        return Err("runtime assertion panel is not enabled for this resource");
    }
    let tenant_id = claims
        .tenant_id
        .as_deref()
        .filter(|value| !value.is_empty() && *value != "platform")
        .map(|value| {
            uuid::Uuid::parse_str(value)
                .ok()
                .filter(|id| !id.is_nil())
                .ok_or("runtime assertion tenant is invalid")
        })
        .transpose()?;
    let (owner_id, owner_type) = tenant_id
        .map(|value| (value.to_string(), "TENANT"))
        .unwrap_or_else(|| (actor_user_id.to_string(), "PERSONAL"));

    let authorization = crate::infra::iam_proto::auth::RuntimeReadAuthorizationRequestV1 {
        actor_user_id: actor_user_id.as_bytes().to_vec(),
        actor_username: claims.sub.clone(),
        tenant_id: tenant_id
            .map(|value| value.as_bytes().to_vec())
            .unwrap_or_default(),
        workspace_id: workspace_id.as_bytes().to_vec(),
        permission: permission.to_string(),
    };
    let decision = shared_redis
        .request(
            AUTHORIZATION_REQUEST_CHANNEL,
            AUTHORIZATION_REPLY_PREFIX,
            authorization.encode_to_vec(),
            Duration::from_millis(900),
        )
        .await
        .map_err(|_| "runtime authorization is unavailable")?;
    let decision = crate::infra::iam_proto::auth::RuntimeReadAuthorizationResponseV1::decode(
        decision.as_slice(),
    )
    .map_err(|_| "runtime authorization response is invalid")?;
    if !decision.allowed {
        return Err("runtime read permission denied");
    }

    let mut path =
        format!("/zone-public/v1/runtime/{module}/{internal_resource_type}/{resource_id}/{panel}");
    if let Some(component) = component_id.as_deref() {
        path.push('/');
        path.push_str(component);
    }
    path.push_str("?from_seconds=");
    path.push_str(&request.from_seconds.to_string());

    let now = chrono::Utc::now();
    let expires_at = now + chrono::Duration::seconds(ASSERTION_TTL_SECONDS);
    let workspace_id_text = workspace_id.to_string();
    let zone_id_text = zone_id.to_string();
    let assertion = RuntimeReadAssertion {
        schema_version: 1,
        jti: uuid::Uuid::new_v4().to_string(),
        actor_id: &claims.uid,
        owner_id: &owner_id,
        owner_type,
        workspace_id: &workspace_id_text,
        zone_id: &zone_id_text,
        module,
        resource_type: internal_resource_type,
        resource_id: resource_id.to_string(),
        panel_id: panel,
        component_id,
        capability: "runtime.read",
        method: "GET",
        path_hash: format!("{:x}", Sha256::digest(path.as_bytes())),
        issued_at: now.timestamp(),
        expires_at: expires_at.timestamp(),
        audience: ASSERTION_AUDIENCE,
        issuer: ASSERTION_ISSUER,
        key_id: &config.vault.zone_control_assertion_key_id,
    };
    let encoded = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(serde_json::to_vec(&assertion).map_err(|_| "runtime assertion encoding failed")?);
    let (key_id, signature) = token_mgr
        .sign_zone_control_assertion(
            &config.vault.zone_control_assertion_key_path,
            encoded.as_bytes(),
        )
        .await
        .map_err(|_| "runtime assertion signing failed")?;
    if key_id != config.vault.zone_control_assertion_key_id {
        return Err("runtime assertion signing key version mismatch");
    }
    Ok(RuntimeReadTicket {
        assertion: encoded,
        signature,
        key_id,
        zone_id: zone_id_text,
        zone_code: zone_code.to_string(),
        method: "GET",
        path,
        expires_at: expires_at.to_rfc3339_opts(chrono::SecondsFormat::Secs, true),
    })
}

// This registry is a security boundary: one public token maps to exactly one
// internal adapter and permission. Keeping the mapping pure makes accidental
// permission/path widening directly testable.
fn runtime_resource_contract(value: &str) -> Option<(&'static str, &'static str, &'static str)> {
    match value {
        "mail_consumer" => Some(("mail", "consumer", "email:consumer:read")),
        "storage_bucket" => Some(("storage", "bucket", "storage:bucket:read")),
        _ => None,
    }
}

fn zone_code_token(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 63
        && value.bytes().enumerate().all(|(index, byte)| {
            byte.is_ascii_lowercase()
                || byte.is_ascii_digit()
                || (byte == b'-' && index > 0 && index + 1 < value.len())
        })
}

fn runtime_component_token(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
}

#[cfg(test)]
#[path = "../tests/unit/runtime_read.rs"]
mod tests;
