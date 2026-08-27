use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use async_nats::jetstream;
use async_nats::jetstream::kv::CreateErrorKind;
use base64::Engine;
use bytes::Bytes;
use ed25519_dalek::{Signature, VerifyingKey};
use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use tokio::sync::Semaphore;
use tonic::Status;
use uuid::Uuid;

const ASSERTION_AUDIENCE: &str = "zone-public-edge-gateway";
const ASSERTION_ISSUER: &str = "aurora-acr";
const MAX_ASSERTION_BYTES: usize = 8 * 1024;

#[derive(Clone)]
pub struct RuntimeReadAuthorizer {
    store: jetstream::kv::Store,
    replay_store: jetstream::kv::Store,
    zone_id: String,
    keys: HashMap<String, VerifyingKey>,
    timeout: Duration,
    inflight: Arc<Semaphore>,
}

#[derive(Debug)]
enum RuntimeReadError {
    Denied(&'static str),
    Unavailable(&'static str),
}

#[derive(Clone, Debug)]
struct RuntimeReadHeaders {
    pub module: String,
    pub resource_type: String,
    pub resource_id: String,
    pub owner_id: String,
    pub workspace_id: String,
    pub zone_id: String,
    pub panel_id: String,
    pub component_id: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Assertion {
    schema_version: u32,
    jti: String,
    actor_id: String,
    owner_id: String,
    owner_type: String,
    workspace_id: String,
    zone_id: String,
    module: String,
    resource_type: String,
    resource_id: String,
    panel_id: String,
    component_id: Option<String>,
    capability: String,
    method: String,
    path_hash: String,
    issued_at: i64,
    expires_at: i64,
    audience: String,
    issuer: String,
    key_id: String,
}

#[derive(Deserialize)]
struct RuntimeResourceHead {
    schema_version: u32,
    runtime_read_enabled: bool,
    module: String,
    resource_type: String,
    resource_id: String,
    version: u64,
    tombstoned: bool,
    owner_id: String,
    owner_type: String,
    workspace_id: String,
    zone_id: String,
}

impl RuntimeReadAuthorizer {
    pub fn new(
        store: jetstream::kv::Store,
        replay_store: jetstream::kv::Store,
        zone_id: String,
        public_keys_json: &str,
        timeout: Duration,
        max_inflight: usize,
    ) -> Result<Self, String> {
        let encoded: HashMap<String, String> = serde_json::from_str(public_keys_json)
            .map_err(|_| "ZONE_RUNTIME_ASSERTION_PUBLIC_KEYS must be a JSON object")?;
        let mut keys = HashMap::with_capacity(encoded.len());
        for (key_id, value) in encoded {
            let bytes = base64::engine::general_purpose::STANDARD
                .decode(value)
                .map_err(|_| "runtime assertion public key is not base64")?;
            let bytes: [u8; 32] = bytes
                .try_into()
                .map_err(|_| "runtime assertion public key must be 32 bytes")?;
            let key = VerifyingKey::from_bytes(&bytes)
                .map_err(|_| "runtime assertion public key is invalid")?;
            keys.insert(key_id, key);
        }
        if keys.is_empty() {
            return Err("runtime assertion public key set is empty".to_string());
        }
        Ok(Self {
            store,
            replay_store,
            zone_id,
            keys,
            timeout,
            inflight: Arc::new(Semaphore::new(max_inflight)),
        })
    }

    pub async fn authorize(
        &self,
        headers: &HashMap<String, String>,
        method: &str,
        path: &str,
    ) -> Result<CheckResponse, Status> {
        let Ok(_permit) = self.inflight.clone().try_acquire_owned() else {
            return Err(Status::resource_exhausted(
                "Zone runtime authorizer overloaded",
            ));
        };
        let runtime = match self.authorize_headers(headers, method, path).await {
            Ok(runtime) => runtime,
            Err(RuntimeReadError::Denied(code)) => {
                tracing::warn!(event_code = code, outcome = "denied");
                return Ok(CheckResponse::with_status(Status::permission_denied(
                    "Runtime read denied",
                )));
            }
            Err(RuntimeReadError::Unavailable(code)) => {
                tracing::error!(event_code = code, outcome = "unavailable");
                return Err(Status::unavailable("Runtime registry unavailable"));
            }
        };
        let mut response = CheckResponse::with_status(Status::ok("authorized"));
        response.set_http_response(
            envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
        );
        if let Some(
            envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse::OkResponse(ok),
        ) = response.http_response.as_mut()
        {
            use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
            let mut headers = vec![
                ("x-aurora-module", runtime.module),
                ("x-aurora-resource-type", runtime.resource_type),
                ("x-aurora-resource-id", runtime.resource_id),
                ("x-aurora-owner-id", runtime.owner_id),
                ("x-aurora-workspace-id", runtime.workspace_id),
                ("x-aurora-zone-id", runtime.zone_id),
                ("x-aurora-panel-id", runtime.panel_id),
            ];
            if let Some(component_id) = runtime.component_id {
                headers.push(("x-aurora-component-id", component_id));
            }
            for (key, value) in headers {
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: key.to_string(),
                        value,
                        ..Default::default()
                    }),
                    append_action: 2,
                    ..Default::default()
                });
            }
            ok.headers_to_remove.extend([
                "x-aurora-runtime-assertion".to_string(),
                "x-aurora-runtime-signature".to_string(),
                "x-aurora-runtime-key-id".to_string(),
            ]);
        }
        Ok(response)
    }

    async fn authorize_headers(
        &self,
        headers: &HashMap<String, String>,
        method: &str,
        path: &str,
    ) -> Result<RuntimeReadHeaders, RuntimeReadError> {
        let assertion = verify_assertion(&self.keys, &self.zone_id, headers, method, path)?;
        let entry = tokio::time::timeout(
            self.timeout,
            self.store.entry(format!(
                "{}.{}.head.{}",
                assertion.module, assertion.resource_type, assertion.resource_id
            )),
        )
        .await
        .map_err(|_| RuntimeReadError::Unavailable("RUNTIME_REGISTRY_TIMEOUT"))?
        .map_err(|_| RuntimeReadError::Unavailable("RUNTIME_REGISTRY_UNAVAILABLE"))?
        .ok_or(RuntimeReadError::Denied("RUNTIME_RESOURCE_NOT_REGISTERED"))?;
        let head: RuntimeResourceHead = serde_json::from_slice(&entry.value)
            .map_err(|_| RuntimeReadError::Unavailable("RUNTIME_REGISTRY_CORRUPT"))?;
        if head.schema_version != 1
            || !head.runtime_read_enabled
            || head.module != assertion.module
            || head.resource_type != assertion.resource_type
            || head.resource_id != assertion.resource_id
            || head.version == 0
            || head.tombstoned
            || head.owner_id != assertion.owner_id
            || head.owner_type != assertion.owner_type
            || head.workspace_id != assertion.workspace_id
            || head.zone_id != assertion.zone_id
        {
            return Err(RuntimeReadError::Denied("RUNTIME_RESOURCE_SCOPE_DENIED"));
        }
        let replay = tokio::time::timeout(
            self.timeout,
            self.replay_store
                .create(assertion.jti, Bytes::from_static(b"1")),
        )
        .await
        .map_err(|_| RuntimeReadError::Unavailable("RUNTIME_REPLAY_FENCE_TIMEOUT"))?;
        if let Err(error) = replay {
            return if error.kind() == CreateErrorKind::AlreadyExists {
                Err(RuntimeReadError::Denied("RUNTIME_ASSERTION_REPLAYED"))
            } else {
                Err(RuntimeReadError::Unavailable(
                    "RUNTIME_REPLAY_FENCE_UNAVAILABLE",
                ))
            };
        }
        Ok(RuntimeReadHeaders {
            module: assertion.module,
            resource_type: assertion.resource_type,
            resource_id: assertion.resource_id,
            owner_id: assertion.owner_id,
            workspace_id: assertion.workspace_id,
            zone_id: assertion.zone_id,
            panel_id: assertion.panel_id,
            component_id: assertion.component_id,
        })
    }
}

// Signature, route and query verification form one transport boundary. Keeping
// it pure makes every scope-widening rule testable without a Zone KV fixture.
fn verify_assertion(
    keys: &HashMap<String, VerifyingKey>,
    zone_id: &str,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Assertion, RuntimeReadError> {
    let encoded = headers
        .get("x-aurora-runtime-assertion")
        .filter(|value| !value.is_empty() && value.len() <= MAX_ASSERTION_BYTES)
        .ok_or(RuntimeReadError::Denied("RUNTIME_ASSERTION_MISSING"))?;
    let signature = headers
        .get("x-aurora-runtime-signature")
        .ok_or(RuntimeReadError::Denied("RUNTIME_SIGNATURE_MISSING"))?;
    let key_id = headers
        .get("x-aurora-runtime-key-id")
        .ok_or(RuntimeReadError::Denied("RUNTIME_KEY_ID_MISSING"))?;
    let key = keys
        .get(key_id)
        .ok_or(RuntimeReadError::Denied("RUNTIME_KEY_UNKNOWN"))?;
    let signature = base64::engine::general_purpose::STANDARD
        .decode(signature)
        .map_err(|_| RuntimeReadError::Denied("RUNTIME_SIGNATURE_ENCODING_INVALID"))?;
    let signature: [u8; 64] = signature
        .try_into()
        .map_err(|_| RuntimeReadError::Denied("RUNTIME_SIGNATURE_SIZE_INVALID"))?;
    key.verify_strict(encoded.as_bytes(), &Signature::from_bytes(&signature))
        .map_err(|_| RuntimeReadError::Denied("RUNTIME_SIGNATURE_INVALID"))?;
    let assertion: Assertion = serde_json::from_slice(
        &base64::engine::general_purpose::URL_SAFE_NO_PAD
            .decode(encoded)
            .map_err(|_| RuntimeReadError::Denied("RUNTIME_ASSERTION_ENCODING_INVALID"))?,
    )
    .map_err(|_| RuntimeReadError::Denied("RUNTIME_ASSERTION_JSON_INVALID"))?;
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| RuntimeReadError::Unavailable("RUNTIME_CLOCK_UNAVAILABLE"))?
        .as_secs() as i64;
    let (path_only, query) = path.split_once('?').unwrap_or((path, ""));
    let route = path_only
        .strip_prefix("/zone-public/v1/runtime/")
        .ok_or(RuntimeReadError::Denied("RUNTIME_ROUTE_INVALID"))?;
    let mut segments = route.split('/');
    let path_module = segments.next().unwrap_or_default();
    let path_resource_type = segments.next().unwrap_or_default();
    let path_resource = segments.next().unwrap_or_default();
    let path_panel = segments.next().unwrap_or_default();
    let path_component = segments.next();
    let mut from_seconds_seen = false;
    for (key, value) in url::form_urlencoded::parse(query.as_bytes()) {
        if key != "from_seconds"
            || from_seconds_seen
            || value
                .parse::<u64>()
                .ok()
                .is_none_or(|seconds| !(1..=300).contains(&seconds))
        {
            return Err(RuntimeReadError::Denied("RUNTIME_QUERY_INVALID"));
        }
        from_seconds_seen = true;
    }
    if assertion.schema_version != 1
        || assertion.key_id != *key_id
        || assertion.audience != ASSERTION_AUDIENCE
        || assertion.issuer != ASSERTION_ISSUER
        || assertion.capability != "runtime.read"
        || assertion.method != method
        || method != "GET"
        || assertion.path_hash != format!("{:x}", Sha256::digest(path.as_bytes()))
        || assertion.issued_at > now.saturating_add(3)
        || assertion.issued_at < now.saturating_sub(15)
        || assertion.expires_at < now.saturating_sub(3)
        || assertion.expires_at > assertion.issued_at.saturating_add(15)
        || assertion.zone_id != zone_id
        || assertion.module != path_module
        || assertion.resource_type != path_resource_type
        || assertion.resource_id != path_resource
        || assertion.panel_id != path_panel
        || assertion.component_id.as_deref() != path_component
        || !from_seconds_seen
        || !runtime_token(path_module, 64)
        || !runtime_token(path_resource_type, 64)
        || path_component.is_some_and(|value| !runtime_component_token(value))
        || !matches!(path_panel, "health" | "metrics" | "logs" | "events")
        || segments.next().is_some()
        || Uuid::parse_str(&assertion.jti).is_err()
        || Uuid::parse_str(&assertion.actor_id).is_err()
        || Uuid::parse_str(&assertion.owner_id).is_err()
        || Uuid::parse_str(&assertion.workspace_id).is_err()
        || Uuid::parse_str(&assertion.resource_id).is_err()
        || !matches!(assertion.owner_type.as_str(), "PERSONAL" | "TENANT")
    {
        return Err(RuntimeReadError::Denied(
            "RUNTIME_ASSERTION_BINDING_INVALID",
        ));
    }
    Ok(assertion)
}

fn runtime_token(value: &str, max_length: usize) -> bool {
    !value.is_empty()
        && value.len() <= max_length
        && value.bytes().enumerate().all(|(index, byte)| {
            byte.is_ascii_lowercase()
                || byte.is_ascii_digit()
                || (index > 0 && matches!(byte, b'_' | b'-'))
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
#[path = "../test/runtime_read.rs"]
mod tests;
