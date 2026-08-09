// ======================================================================================================
// 📂 billing/exchange.rs — One-time handoff tạo host-only alias cho Cost Console
// ======================================================================================================

use crate::billing::session::{release_billing_alias, ReleaseBillingAliasCommand};
use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::pkg::cookie::{COOKIE_BILLING_SESSION_ID, COOKIE_BILLING_SESSION_SECRET};
use crate::user::claims::Claims;
use crate::user::session::UserAccessSession;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::{CheckRequest, CheckResponse};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};
use uuid::Uuid;

const HANDOFF_TTL_SECONDS: u64 = 60;

#[derive(Debug, Serialize, Deserialize)]
struct BillingHandoffRecord {
    user_id: String,
    username: String,
    zone_id: String,
    tenant_id: String,
    source_access_key: String,
    client_proof_public_key: String,
    state: String,
    code_challenge: String,
}

#[derive(Deserialize)]
struct BillingHandoffIssuePayload {
    state: String,
    code_challenge: String,
}

#[derive(Deserialize)]
struct BillingExchangePayload {
    handoff_code: String,
    code_verifier: String,
    device_public_key: String,
}

fn handoff_redis_key(code: &str) -> String {
    use sha2::{Digest, Sha256};
    let digest = Sha256::digest(code.as_bytes());
    format!("billing:handoff:{digest:x}")
}

fn raw_body(req: &CheckRequest) -> Vec<u8> {
    req.attributes
        .as_ref()
        .and_then(|attributes| attributes.request.as_ref())
        .and_then(|request| request.http.as_ref())
        .map(|http| {
            if http.body.is_empty() {
                http.raw_body.clone()
            } else {
                http.body.as_bytes().to_vec()
            }
        })
        .unwrap_or_default()
}

fn local_json(status: HttpStatusCode, body: serde_json::Value, message: &str) -> CheckResponse {
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(status);
    builder.add_header("content-type", "application/json", None, false);
    builder.add_header("cache-control", "no-store", None, false);
    builder.set_body(body.to_string());
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(builder);
    response
}

fn denied(status: HttpStatusCode, message: &str) -> Response<CheckResponse> {
    Response::new(local_json(
        status,
        serde_json::json!({"error_message": message}),
        message,
    ))
}

pub struct BillingHandoffWorkflowContext<'a> {
    pub session_mgr: &'a Arc<SessionManager>,
    pub config: &'a Config,
}

pub struct BillingHandoffIssueRequest<'a> {
    pub claims: &'a Claims,
    pub access_key: &'a str,
    pub source_session: &'a UserAccessSession,
    pub request: &'a CheckRequest,
    pub method: &'a str,
    pub path: &'a str,
}

/// [COMMENT]: Source Cloud Console xin opaque handoff code sau khi IAM Trinity, CSRF, zone và tenant đã verify.
pub async fn handle_billing_handoff_issue(
    workflow: BillingHandoffWorkflowContext<'_>,
    request: BillingHandoffIssueRequest<'_>,
) -> Option<Result<Response<CheckResponse>, Status>> {
    let BillingHandoffWorkflowContext {
        session_mgr,
        config,
    } = workflow;
    let BillingHandoffIssueRequest {
        claims,
        access_key,
        source_session,
        request: req,
        method,
        path,
    } = request;
    if !(method == "POST" && path == "/api/v1/auth/domain-sessions/billing") {
        return None;
    }

    let zone_id = match claims
        .zone_id
        .as_deref()
        .and_then(|value| Uuid::parse_str(value).ok())
    {
        Some(zone_id) => zone_id.to_string(),
        None => {
            return Some(Ok(denied(
                HttpStatusCode::Forbidden,
                "A concrete zone is required for Billing Console",
            )))
        }
    };
    if access_key.is_empty() || source_session.client_proof_public_key.trim().is_empty() {
        return Some(Ok(denied(
            HttpStatusCode::Unauthorized,
            "Source IAM session is incomplete",
        )));
    }
    let issue_payload: BillingHandoffIssuePayload =
        match serde_json::from_slice::<BillingHandoffIssuePayload>(&raw_body(req)) {
            Ok(payload)
                if (32..=128).contains(&payload.state.len())
                    && payload.state.bytes().all(|byte| {
                        byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_'
                    })
                    && payload.code_challenge.len() == 43
                    && payload.code_challenge.bytes().all(|byte| {
                        byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_'
                    }) =>
            {
                payload
            }
            _ => {
                return Some(Ok(denied(
                    HttpStatusCode::BadRequest,
                    "A valid PKCE state and code_challenge are required",
                )))
            }
        };

    let code = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
    let record = BillingHandoffRecord {
        user_id: claims.uid.clone(),
        username: claims.sub.clone(),
        zone_id,
        tenant_id: claims
            .tenant_id
            .clone()
            .unwrap_or_else(|| "platform".to_string()),
        source_access_key: access_key.to_string(),
        client_proof_public_key: source_session.client_proof_public_key.clone(),
        state: issue_payload.state,
        code_challenge: issue_payload.code_challenge,
    };
    let encoded = match serde_json::to_vec(&record) {
        Ok(encoded) => encoded,
        Err(error) => return Some(Err(Status::internal(error.to_string()))),
    };
    let mut conn = match session_mgr.get_connection().await {
        Ok(conn) => conn,
        Err(error) => return Some(Err(Status::unavailable(error.to_string()))),
    };
    let stored: Option<String> = match redis::cmd("SET")
        .arg(handoff_redis_key(&code))
        .arg(encoded)
        .arg("NX")
        .arg("EX")
        .arg(HANDOFF_TTL_SECONDS)
        .query_async(&mut conn)
        .await
    {
        Ok(stored) => stored,
        Err(error) => return Some(Err(Status::unavailable(error.to_string()))),
    };
    if stored.as_deref() != Some("OK") {
        return Some(Err(Status::unavailable("Unable to reserve handoff code")));
    }

    // [COMMENT]: Code nằm trong fragment nên browser không gửi nó trong HTTP request, access log hay Referer.
    let redirect_url = format!(
        "{}/auth/handoff#code={}&state={}",
        config.billing_console_origin, code, record.state
    );
    Some(Ok(Response::new(local_json(
        HttpStatusCode::Ok,
        serde_json::json!({"data": {"redirect_url": redirect_url}}),
        "Billing handoff issued",
    ))))
}

/// [COMMENT]: Cost redeem code đúng một lần và nhận alias; không render quyền, không phát Billing JWT.
pub async fn handle_billing_handoff_exchange(
    session_mgr: &Arc<SessionManager>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    req: &CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/api/v1/billing/auth/exchange") {
        return None;
    }
    if !crate::gateway::csrf::verify_csrf_protection(method, client_headers) {
        return Some(Ok(denied(
            HttpStatusCode::Forbidden,
            "CSRF validation failed",
        )));
    }

    let payload: BillingExchangePayload =
        match serde_json::from_slice::<BillingExchangePayload>(&raw_body(req)) {
            Ok(payload)
                if payload.handoff_code.len() == 64
                    && (43..=128).contains(&payload.code_verifier.len()) =>
            {
                payload
            }
            _ => {
                return Some(Ok(denied(
                    HttpStatusCode::BadRequest,
                    "A valid handoff code is required",
                )))
            }
        };
    use base64::Engine;
    let device_public_key =
        match base64::engine::general_purpose::STANDARD.decode(payload.device_public_key.trim()) {
            Ok(bytes) if bytes.len() == 32 => payload.device_public_key.trim().to_string(),
            _ => {
                return Some(Ok(denied(
                    HttpStatusCode::BadRequest,
                    "device_public_key must be a Base64 Ed25519 public key",
                )))
            }
        };

    let mut conn = match session_mgr.get_connection().await {
        Ok(conn) => conn,
        Err(error) => return Some(Err(Status::unavailable(error.to_string()))),
    };
    // [COMMENT]: GETDEL là atomic consume; retry/replay của cùng code luôn thất bại.
    let encoded: Option<Vec<u8>> = match redis::cmd("GETDEL")
        .arg(handoff_redis_key(&payload.handoff_code))
        .query_async(&mut conn)
        .await
    {
        Ok(encoded) => encoded,
        Err(error) => return Some(Err(Status::unavailable(error.to_string()))),
    };
    let record: BillingHandoffRecord = match encoded
        .as_deref()
        .and_then(|bytes| serde_json::from_slice(bytes).ok())
    {
        Some(record) => record,
        None => {
            return Some(Ok(denied(
                HttpStatusCode::Unauthorized,
                "Handoff code expired or was already consumed",
            )))
        }
    };
    use sha2::{Digest, Sha256};
    let verifier_challenge = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(Sha256::digest(payload.code_verifier.as_bytes()));
    if verifier_challenge != record.code_challenge {
        return Some(Ok(denied(
            HttpStatusCode::Unauthorized,
            "PKCE verifier does not match this handoff",
        )));
    }

    // [COMMENT]: One-time code không sống lâu hơn source session; logout trong lúc redirect làm exchange fail-closed.
    match session_mgr
        .get_session(
            &record.zone_id,
            &record.tenant_id,
            &record.user_id,
            &record.source_access_key,
        )
        .await
    {
        Ok(Some(session)) if session.client_proof_public_key == record.client_proof_public_key => {}
        Ok(_) => {
            return Some(Ok(denied(
                HttpStatusCode::Unauthorized,
                "Source IAM session expired or was revoked",
            )))
        }
        Err(error) => return Some(Err(Status::unavailable(error.to_string()))),
    }

    let released = match release_billing_alias(
        session_mgr,
        ReleaseBillingAliasCommand {
            user_id: &record.user_id,
            username: &record.username,
            zone_id: &record.zone_id,
            tenant_id: &record.tenant_id,
            source_access_key: &record.source_access_key,
            source_proof_public_key: &record.client_proof_public_key,
            client_proof_public_key: &device_public_key,
        },
    )
    .await
    {
        Ok(released) => released,
        Err(error) => return Some(Err(error)),
    };

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::NoContent);
    for cookie in [
        format!(
            "{COOKIE_BILLING_SESSION_ID}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
            released.alias_id, config.session_ttl_secs
        ),
        format!(
            "{COOKIE_BILLING_SESSION_SECRET}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
            released.alias_secret, config.session_ttl_secs
        ),
    ] {
        builder.add_header("set-cookie", &cookie, None, false);
    }
    builder.add_header("cache-control", "no-store", None, false);
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Billing handoff exchanged"));
    response.set_http_response(builder);
    Some(Ok(Response::new(response)))
}
