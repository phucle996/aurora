// ======================================================================================================
// 📂 user/login.rs — Intercept POST /api/v1/auth/login + release_user_session
//
// 📌 LUỒNG:
//   POST /api/v1/auth/login
//   1. Parse JSON payload (username, password, device info, zone_code)
//   2. Gọi NATS iam.auth.verify_credentials sang Controlplane
//   3. Cấp phát Trinity Session cho User via release_user_session
//   4. Trả về Set-Cookie HTTP response
// ======================================================================================================

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::nats::auth::{
    EvictedDevicesNotification, VerifyUserCredentialsRequest, VerifyUserCredentialsResponse,
};
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::user::claims::Claims;
use async_nats::HeaderMap;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tonic::{Response, Status};
use uuid::Uuid;

/// [COMMENT]: JSON payload nhận từ client khi User đăng nhập
#[derive(Deserialize)]
pub struct LoginPayload {
    pub username: Option<String>,
    pub password: Option<String>,
    pub device_name: Option<String>,
    pub device_type: Option<String>,
    pub public_key: Option<String>,
    pub signature: Option<String>,
    pub trust_device: Option<bool>,
    pub zone_code: Option<String>,
}

/// [COMMENT]: Response JSON lỗi chung
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_code: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub verification_email_queued: Option<bool>,
}

/// [COMMENT]: Kết quả cấp phát Trinity Session cho User
pub struct ReleaseUserSessionResult {
    pub access_token: String,
    pub access_key: String,
    pub access_secret: String,
    pub client_device_id: String,
    pub tenant_id_val: String,
}

/// [COMMENT]: Khởi tạo Trinity Session riêng cho User (dùng cho cả HTTP Login và gRPC Release)
pub async fn release_user_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    nats: &Nats,
    redis_client: &redis::Client,
    config: &Config,
    user_id: &str,
    username: &str,
    role: &str,
    level: i32,
    tenant_id: &str,
    zone_id: &str,
    device_id: &str,
    client_device_id: &str,
    _trust_device: bool,
    _refresh_token: &str,
) -> Result<ReleaseUserSessionResult, Status> {
    Logger::sys_info(
        "user.login.release",
        &format!("Releasing new user trinity session for user_id={}", user_id),
    );

    let access_key = Uuid::now_v7().to_string();
    let access_secret = Uuid::new_v4().to_string();
    let ash = sha256_hash(&access_secret);

    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;

    let claims = Claims {
        sub: username.to_string(),
        uid: user_id.to_string(),
        role_id: role.to_string(),
        lvl: level,
        tenant_id: if tenant_id.is_empty() {
            None
        } else {
            Some(tenant_id.to_string())
        },
        zone_id: if zone_id.is_empty() {
            None
        } else if Uuid::parse_str(zone_id).is_ok() {
            Some(zone_id.to_string())
        } else {
            crate::infra::zone::resolve_code_to_id_and_status(nats, redis_client, zone_id)
                .await
                .map(|(id, _)| id)
        },
        access_key: access_key.clone(),
        jti: Uuid::new_v4().to_string(),
        iss: Some("aurora-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    let access_token = match token_mgr.generate_token(&claims).await {
        Ok(token) => token,
        Err(e) => {
            Logger::sys_error(
                "user.login.release",
                "Failed to sign access token via Vault",
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Failed to sign access token: {}",
                e
            )));
        }
    };

    let evicted_tdids = match session_mgr
        .register_session(
            claims.zone_id.as_deref().unwrap_or("global"),
            claims.tenant_id.as_deref().unwrap_or("platform"),
            user_id,
            &access_key,
            &ash,
            device_id,
        )
        .await
    {
        Ok(ev) => ev,
        Err(e) => {
            Logger::sys_error(
                "user.login.release",
                "Failed to register session state in Redis L2",
                &e.to_string(),
            );
            return Err(Status::internal("Failed to save session state"));
        }
    };

    if !evicted_tdids.is_empty() {
        let notification = EvictedDevicesNotification {
            user_id: user_id.to_string(),
            client_device_ids: evicted_tdids.clone(),
        };
        let mut buf = Vec::new();
        if notification.encode(&mut buf).is_ok() {
            let _ = nats
                .client()
                .publish("iam.device.evicted".to_string(), buf.into())
                .await;
            Logger::sys_info(
                "user.login.release",
                &format!(
                    "Published evicted notification for user {} on {} devices",
                    user_id,
                    evicted_tdids.len()
                ),
            );
        }
    }

    Ok(ReleaseUserSessionResult {
        access_token,
        access_key,
        access_secret,
        client_device_id: client_device_id.to_string(),
        tenant_id_val: claims.tenant_id.unwrap_or_else(|| "platform".to_string()),
    })
}

/// [COMMENT]: Intercept POST /api/v1/auth/login tại Edge.
pub async fn handle_login(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    nats: &Arc<Nats>,
    redis_client: &redis::Client,
    config: &Config,
    _client_headers: &std::collections::HashMap<String, String>,
    req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/api/v1/auth/login") {
        return None;
    }

    Logger::sys_info("user.login", "Intercepted login request at edge");

    let raw_body_bytes = req
        .attributes
        .as_ref()
        .and_then(|a| a.request.as_ref())
        .and_then(|r| r.http.as_ref())
        .map(|h| {
            if !h.body.is_empty() {
                h.body.as_bytes().to_vec()
            } else {
                h.raw_body.clone()
            }
        })
        .unwrap_or_default();

    if raw_body_bytes.is_empty() {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::BadRequest,
            "Request body is empty",
        ))));
    }

    let payload: LoginPayload = match serde_json::from_slice(&raw_body_bytes) {
        Ok(p) => p,
        Err(e) => {
            Logger::sys_warn(
                "user.login",
                &format!("Failed to parse JSON body: {}", e),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Invalid JSON payload format",
            ))));
        }
    };

    let username = match payload.username {
        Some(ref u) if !u.trim().is_empty() => u.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Username is required",
            ))));
        }
    };

    let password = match payload.password {
        Some(ref p) if !p.is_empty() => p.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Password is required",
            ))));
        }
    };

    let client_device_id = Uuid::new_v4().to_string();
    let zone_code = payload.zone_code.as_deref().unwrap_or("global");

    let client_ip = _client_headers
        .get("x-forwarded-for")
        .cloned()
        .unwrap_or_default();
    let user_agent = _client_headers
        .get("user-agent")
        .cloned()
        .or_else(|| _client_headers.get("User-Agent").cloned())
        .unwrap_or_default();
    let tenant_domain = if let Some(idx) = username.find('@') {
        username[idx + 1..].to_string()
    } else {
        String::new()
    };

    let cp_req = VerifyUserCredentialsRequest {
        username: username.clone(),
        password,
        device_name: payload.device_name.unwrap_or_default(),
        device_type: payload.device_type.unwrap_or_default(),
        public_key: payload.public_key.unwrap_or_default(),
        signature: payload.signature.unwrap_or_default(),
        client_device_id: client_device_id.clone(),
        trust_device: payload.trust_device.unwrap_or(false),
        client_ip,
        user_agent,
        tenant_domain,
    };

    Logger::sys_info(
        "user.login",
        // [COMMENT]: Username là identity data; correlation dùng trace/request metadata thay vì raw username.
        "Forwarding credentials verification request to Controlplane",
    );

    let mut payload_bytes = Vec::new();
    if let Err(e) = cp_req.encode(&mut payload_bytes) {
        Logger::sys_error(
            "user.login",
            "Failed to serialize verification request",
            &e.to_string(),
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::InternalServerError,
            "Internal server error",
        ))));
    }

    let mut headers = HeaderMap::new();
    if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
        let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
        let traceparent = format!("00-{}-{}-01", trace_id, span_id);
        headers.insert("traceparent", traceparent.as_str());
    }

    let response_msg = match nats
        .client()
        .request_with_headers(
            "iam.auth.verify_credentials".to_string(),
            headers,
            payload_bytes.into(),
        )
        .await
    {
        Ok(msg) => msg,
        Err(e) => {
            Logger::sys_error(
                "user.login",
                "NATS credentials verification request failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service unavailable",
            ))));
        }
    };

    let cp_res = match VerifyUserCredentialsResponse::decode(response_msg.payload.as_ref()) {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "user.login",
                "VerifyCredentialsResponse decoding failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service returned invalid data",
            ))));
        }
    };

    if !cp_res.valid {
        let err_msg = if cp_res.error_message.is_empty() {
            "Invalid username or password".to_string()
        } else {
            cp_res.error_message.clone()
        };
        Logger::sys_warn(
            "user.login",
            &format!("Authentication rejected: {}", err_msg),
            "",
        );
        if err_msg == "ACCOUNT_VERIFICATION_REQUIRED" {
            return Some(Ok(Response::new(build_verification_required_json())));
        }
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            &err_msg,
        ))));
    }

    let res_val = match release_user_session(
        session_mgr,
        token_mgr,
        nats,
        redis_client,
        config,
        &cp_res.user_id,
        &username,
        &cp_res.role_id,
        cp_res.level,
        &cp_res.tenant_id,
        zone_code,
        &cp_res.client_device_id,
        &cp_res.client_device_id,
        payload.trust_device.unwrap_or(false),
        &cp_res.refresh_token,
    )
    .await
    {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error("user.login", "Release user session failed", &e.to_string());
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue session state",
            ))));
        }
    };

    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    let access_cookie = format!(
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_token, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    let key_cookie = format!(
        "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_key, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    let secret_cookie = format!(
        "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_secret, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    let device_cookie = format!(
        "client_device_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        res_val.client_device_id, domain_str
    );
    denied_builder.add_header("set-cookie", &device_cookie, None, false);

    let tenant_cookie = format!(
        "tenant_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        res_val.tenant_id_val, domain_str
    );
    denied_builder.add_header("set-cookie", &tenant_cookie, None, false);

    if let Some(ref zone_code) = payload.zone_code {
        let zone_cookie = format!(
            "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            zone_code, domain_str
        );
        denied_builder.add_header("set-cookie", &zone_cookie, None, false);
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Local intercept login success"));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "user.login",
        &format!("User session released for user_id={}", cp_res.user_id),
    );

    Some(Ok(Response::new(response)))
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let err_resp = ErrorResponse {
        error_message: message.to_string(),
        error_code: None,
        verification_email_queued: None,
    };
    let json_body = serde_json::to_string(&err_resp).unwrap_or_default();

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(status);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(denied_builder);
    response
}

fn build_verification_required_json() -> CheckResponse {
    // [COMMENT]: 412 biểu diễn credentials đúng nhưng account chưa thỏa activation precondition; tuyệt đối không cấp cookie/session.
    let err_resp = ErrorResponse {
		error_message: "Account verification required. A verification email has been queued if cooldown allows.".to_string(),
		error_code: Some("ACCOUNT_VERIFICATION_REQUIRED".to_string()),
		verification_email_queued: Some(true),
	};
    let json_body = serde_json::to_string(&err_resp).unwrap_or_default();
    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::PreconditionFailed);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);
    let mut response = CheckResponse::new();
    response.set_status(Status::failed_precondition("ACCOUNT_VERIFICATION_REQUIRED"));
    response.set_http_response(denied_builder);
    response
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
