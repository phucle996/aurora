// ======================================================================================================
// 📂 user/login.rs — Intercept POST /api/v1/auth/login + release_user_session
//
// 📌 LUỒNG:
//   POST /api/v1/auth/login
//   1. Parse JSON payload (username, password, device info, zone_code)
//   2. Gọi Shared Redis iam.auth.verify_credentials sang Controlplane
//   3. Cấp phát Trinity Session cho User via release_user_session
//   4. Trả về Set-Cookie HTTP response
// ======================================================================================================

use crate::config::Config;
use crate::infra::iam_proto::auth::{VerifyUserCredentialsRequest, VerifyUserCredentialsResponse};
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::COOKIE_REFRESH_TOKEN;
use crate::token::TokenManager;
use crate::user::claims::Claims;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use std::time::Duration;
use tonic::{Response, Status};
use uuid::Uuid;

/// [COMMENT]: JSON payload nhận từ client khi User đăng nhập
#[derive(Deserialize)]
pub struct LoginPayload {
    pub username: Option<String>,
    pub password: Option<String>,
    pub device_name: Option<String>,
    pub device_type: Option<String>,
    // [COMMENT]: Wire contract chỉ chấp nhận tên canonical, không duy trì alias public_key legacy.
    pub device_public_key: Option<String>,
    pub session_proof_challenge_id: Option<String>,
    pub session_proof_timestamp: Option<i64>,
    pub session_proof_signature: Option<String>,
    pub trust_device: Option<bool>,
    pub zone_code: Option<String>,
    // [COMMENT]: Wire contract canonical luôn tách tenant domain khỏi username; UI có thể giữ cú pháp nhập user@domain.
    pub tenant_domain: Option<String>,
}

// [COMMENT]: Login challenge là public nhưng vẫn đi qua CORS + pre-auth rate limit của ExtAuthz.
pub async fn handle_login_challenge(
    session_mgr: &Arc<SessionManager>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chuẩn hóa path bằng cách bỏ query string và trailing slash để tránh khớp trượt endpoint
    let clean_path = path.split('?').next().unwrap_or(path).trim_end_matches('/');
    if !(method.eq_ignore_ascii_case("POST")
        && (clean_path == "/api/v1/auth/login/challenge" || path == "/api/v1/auth/login/challenge"))
    {
        return None;
    }
    let challenge = match crate::user::session_proof::issue_login_challenge(session_mgr).await {
        Ok(challenge) => challenge,
        Err(error) => {
            Logger::sys_error(
                "user.login.challenge",
                "Failed to issue login proof",
                &error,
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service unavailable",
            ))));
        }
    };
    let body = serde_json::to_string(&challenge).unwrap_or_default();
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(body);
    let mut response = CheckResponse::new();
    // [COMMENT]: Sử dụng Status::unauthenticated để Envoy coi đây là phản hồi trực tiếp tại Edge (UAEX) thay vì forward lên upstream
    response.set_status(Status::unauthenticated(
        "login session proof challenge issued",
    ));
    response.set_http_response(builder);
    Some(Ok(Response::new(response)))
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

// [COMMENT]: Chuẩn hóa identity tại biên và loại bỏ hoàn toàn contract legacy username@tenant_domain trên wire.
fn canonicalize_login_identity(
    username: Option<&str>,
    tenant_domain: Option<&str>,
) -> Result<(String, String), &'static str> {
    let username = username.map(str::trim).filter(|value| !value.is_empty());
    let username = match username {
        Some(value) if !value.contains('@') => value.to_lowercase(),
        Some(_) => return Err("Username must not contain tenant domain"),
        None => return Err("Username is required"),
    };
    let tenant_domain = tenant_domain
        .map(str::trim)
        .filter(|domain| !domain.is_empty())
        .map(str::to_lowercase)
        .unwrap_or_default();
    Ok((username, tenant_domain))
}

/// [COMMENT]: Khởi tạo Trinity Session riêng cho User (dùng cho cả HTTP Login và gRPC Release)
pub async fn release_user_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    user_id: &str,
    username: &str,
    role: &str,
    level: i32,
    tenant_id: &str,
    zone_id: &str,
    device_id: &str,
    client_device_id: &str,
    client_proof_public_key: &str,
) -> Result<ReleaseUserSessionResult, Status> {
    Logger::sys_info(
        "user.login.release",
        &format!("Releasing new user trinity session for user_id={}", user_id),
    );

    // All user entrypoints resolve a concrete Zone before reaching the issuer.
    // Keep this final invariant so no future user flow can mint a global session.
    let resolved_zone_id = Uuid::parse_str(zone_id)
        .ok()
        .filter(|value| !value.is_nil())
        .ok_or_else(|| Status::invalid_argument("User session requires a concrete zone"))?;

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
        zone_id: Some(resolved_zone_id.to_string()),
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

    if let Err(e) = session_mgr
        .register_session(
            claims
                .zone_id
                .as_deref()
                .expect("validated user zone must be present"),
            claims.tenant_id.as_deref().unwrap_or("platform"),
            user_id,
            &access_key,
            &ash,
            device_id,
            client_proof_public_key,
        )
        .await
    {
        Logger::sys_error(
            "user.login.release",
            "Failed to register session state in Auth Redis",
            &e.to_string(),
        );
        return Err(Status::internal("Failed to save session state"));
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
    redis_client: &redis::Client,
    shared_redis: &Arc<SharedRedisBus>,
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

    let (username, tenant_domain) = match canonicalize_login_identity(
        payload.username.as_deref(),
        payload.tenant_domain.as_deref(),
    ) {
        Ok(identity) => identity,
        Err(message) => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                message,
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
    let zone_code = payload
        .zone_code
        .as_deref()
        .map(str::trim)
        .unwrap_or_default()
        .to_ascii_lowercase();
    if zone_code.is_empty() || zone_code == "global" || zone_code.len() > 64 {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::BadRequest,
            "A concrete zone_code is required",
        ))));
    }
    let (resolved_zone_id, zone_status) = match crate::infra::zone::resolve_code_to_id_and_status(
        shared_redis,
        redis_client,
        &zone_code,
    )
    .await
    {
        Some(zone) => zone,
        None => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Forbidden,
                "Zone unavailable",
            ))))
        }
    };
    if !matches!(
        Uuid::parse_str(&resolved_zone_id),
        Ok(value) if !value.is_nil()
    ) || (zone_status != "active" && zone_status != "draining")
    {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Zone unavailable",
        ))));
    }
    let public_key = payload.device_public_key.as_deref().unwrap_or_default();
    if let Err(error) = crate::user::session_proof::verify_login_proof(
        session_mgr,
        payload
            .session_proof_challenge_id
            .as_deref()
            .unwrap_or_default(),
        payload.session_proof_timestamp.unwrap_or_default(),
        &username,
        &tenant_domain,
        &zone_code,
        payload.trust_device.unwrap_or(false),
        public_key,
        payload
            .session_proof_signature
            .as_deref()
            .unwrap_or_default(),
    )
    .await
    {
        Logger::sys_warn("user.login.proof", "Login session proof rejected", &error);
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Invalid login session proof",
        ))));
    }

    let client_ip = _client_headers
        .get("x-forwarded-for")
        .cloned()
        .unwrap_or_default();
    let user_agent = _client_headers
        .get("user-agent")
        .cloned()
        .or_else(|| _client_headers.get("User-Agent").cloned())
        .unwrap_or_default();
    let activity_device_type = payload.device_type.clone().unwrap_or_default();
    let cp_req = VerifyUserCredentialsRequest {
        username: username.clone(),
        password,
        device_name: payload.device_name.unwrap_or_default(),
        device_type: payload.device_type.unwrap_or_default(),
        public_key: public_key.to_string(),
        signature: payload.session_proof_signature.unwrap_or_default(),
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

    // [COMMENT]: ACR đăng ký reply waiter trước khi publish. CP replica winner được
    // chọn bằng SETNX request_id nên login chỉ có đúng một durable side effect.
    let response_payload = match shared_redis
        .request(
            "iam.auth.verify_credentials",
            "iam.auth.verify_credentials.reply.",
            payload_bytes,
            Duration::from_secs(10),
        )
        .await
    {
        Ok(payload) => payload,
        Err(e) => {
            Logger::sys_error(
                "user.login",
                "Shared Redis credentials verification request failed",
                &e,
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service unavailable",
            ))));
        }
    };

    let cp_res = match VerifyUserCredentialsResponse::decode(response_payload.as_slice()) {
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

    let trust_device = payload.trust_device.unwrap_or(false);
    let refresh_cookie_max_age = if trust_device {
        // [COMMENT]: IAM là issuer/durable owner; ACR chỉ phát cookie theo đúng expiry IAM đã commit.
        let max_age = cp_res.refresh_token_expires_at - chrono::Utc::now().timestamp();
        if cp_res.refresh_token.is_empty() || max_age <= 0 {
            Logger::sys_error(
                "user.login",
                "IAM returned invalid refresh token contract",
                "trusted login requires token and future expiry",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue refresh session",
            ))));
        }
        Some(max_age)
    } else {
        None
    };

    let res_val = match release_user_session(
        session_mgr,
        token_mgr,
        config,
        &cp_res.user_id,
        &username,
        &cp_res.role_id,
        cp_res.level,
        &cp_res.tenant_id,
        &resolved_zone_id,
        &cp_res.client_device_id,
        &cp_res.client_device_id,
        &cp_res.client_proof_public_key,
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

    let activity_id = Uuid::now_v7();
    let activity = crate::activity_proto::UserActivityEvent {
        event_id: activity_id.to_string(),
        user_id: cp_res.user_id.clone(),
        category: "security".to_string(),
        action: "session.login".to_string(),
        actor_type: "self".to_string(),
        actor_id: cp_res.user_id.clone(),
        outcome: "succeeded".to_string(),
        source_service: "acr".to_string(),
        resource_type: "session".to_string(),
        resource_id: res_val.client_device_id.clone(),
        operation_id: activity_id.to_string(),
        title: "Signed in".to_string(),
        summary: "A new session was created".to_string(),
        occurred_at: chrono::Utc::now().timestamp(),
        metadata_json: serde_json::json!({
            "device_type": activity_device_type,
            "zone_code": zone_code,
            "remember_device": trust_device,
        })
        .to_string(),
        schema_version: 1,
        trace_parent: String::new(),
        trace_state: String::new(),
    };
    // History is a separate durable stream. A Redis outage must not turn an
    // already-issued authentication session into a false login failure.
    if let Err(error) = shared_redis.append_user_activity(activity).await {
        Logger::sys_error(
            "user.login.activity",
            "Failed to enqueue self activity after successful login",
            &error,
        );
    }

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

    if let Some(max_age) = refresh_cookie_max_age {
        // [COMMENT]: Chỉ nhánh user remember-me nhận opaque refresh token; JS không bao giờ đọc được cookie này.
        let refresh_cookie = format!(
            "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            COOKIE_REFRESH_TOKEN, cp_res.refresh_token, max_age, domain_str
        );
        denied_builder.add_header("set-cookie", &refresh_cookie, None, false);
    }

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

    let zone_cookie = format!(
        "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        zone_code, domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

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

#[cfg(test)]
mod tests {
    use super::canonicalize_login_identity;

    #[test]
    fn canonical_tenant_login_uses_separate_fields() {
        let identity = canonicalize_login_identity(Some(" Alice "), Some(" ACME.Example "));
        assert_eq!(
            identity,
            Ok(("alice".to_string(), "acme.example".to_string()))
        );
    }

    #[test]
    fn legacy_combined_username_is_rejected() {
        assert_eq!(
            canonicalize_login_identity(Some("alice@acme.example"), None),
            Err("Username must not contain tenant domain")
        );
    }
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
