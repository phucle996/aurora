// ======================================================================================================
// 📂 sre/login.rs — handle_admin_login: Điều hướng đăng nhập SRE Admin tại Edge
//
// 📌 LUỒNG:
//   POST /admin/auth/login
//   1. Parse JSON: api_key + totp_code + device_public_key (optional)
//   2. Xác thực api_key hash với Vault secret
//   3. Xác thực TOTP qua Vault TOTP Secrets Engine
//   4. Cấp phát Trinity Session SRE (namespace iam:admin_access_session:)
//   5. Trả về Set-Cookie HTTP response (Path=/admin)
// ======================================================================================================

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::sre::claims::SreTokenManager;
use crate::sre::session::release_sre_session;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tonic::{Response, Status};

/// [COMMENT]: JSON payload nhận từ client khi SRE Admin đăng nhập
#[derive(Deserialize)]
#[allow(dead_code)]
pub struct AdminLoginPayload {
    pub api_key: Option<String>,
    pub totp_code: Option<String>,
    // [COMMENT]: Khóa công khai Ed25519 của thiết bị/trình duyệt đăng nhập
    pub device_public_key: Option<String>,
}

/// [COMMENT]: JSON response lỗi chung
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

pub struct AdminLoginWorkflowContext<'a> {
    pub session_mgr: &'a Arc<SessionManager>,
    pub token_mgr: &'a Arc<SreTokenManager>,
    pub config: &'a Config,
}

pub struct AdminLoginRequest<'a> {
    pub request: &'a envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    pub method: &'a str,
    pub path: &'a str,
}

/// [COMMENT]: Hàm xử lý đăng nhập SRE Admin cục bộ tại biên.
/// Intercept: POST /admin/auth/login
pub async fn handle_admin_login(
    workflow: AdminLoginWorkflowContext<'_>,
    request: AdminLoginRequest<'_>,
) -> Option<Result<Response<CheckResponse>, Status>> {
    let AdminLoginWorkflowContext {
        session_mgr,
        token_mgr,
        config,
    } = workflow;
    let AdminLoginRequest {
        request: req,
        method,
        path,
    } = request;
    // [COMMENT]: Chỉ intercept HTTP POST /admin/auth/login
    if !(method == "POST" && path == "/admin/auth/login") {
        return None;
    }

    Logger::sys_info("sre.login", "Intercepted SRE Admin login request at edge");

    // [COMMENT]: 1. Trích xuất Request Body thô từ Envoy (hỗ trợ cả text body và raw_body)
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

    // [COMMENT]: 2. Giải mã JSON payload
    let payload: AdminLoginPayload = match serde_json::from_slice(&raw_body_bytes) {
        Ok(p) => p,
        Err(e) => {
            Logger::sys_warn(
                "sre.login",
                &format!("Failed to parse Admin login JSON body: {}", e),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Invalid Admin JSON payload format",
            ))));
        }
    };

    let api_key = match payload.api_key {
        Some(ref k) if !k.trim().is_empty() => k.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "API key is required",
            ))));
        }
    };

    let totp_code = match payload.totp_code {
        Some(ref t) if !t.trim().is_empty() => t.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "TOTP code is required",
            ))));
        }
    };

    Logger::sys_info(
        "sre.login",
        &format!(
            "Attempting SRE verification with TOTP length: {}",
            totp_code.len()
        ),
    );

    // [COMMENT]: 3. Lấy Admin API Key Hash từ Vault (có L1 cache bên trong TokenManager)
    let admin_api_key_hash = match token_mgr.get_admin_api_key_hash().await {
        Ok(hash) => hash,
        Err(e) => {
            Logger::sys_error(
                "sre.login",
                "Failed to retrieve admin api key hash",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to retrieve admin credentials configuration",
            ))));
        }
    };

    // [COMMENT]: 4. Tính SHA-256 của api_key input và so khớp với hash trong Vault
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(api_key.as_bytes());
    let input_hash = format!("{:x}", hasher.finalize());

    if input_hash != admin_api_key_hash {
        Logger::sys_warn("sre.login", "SRE Admin login failed: API Key mismatch", "");
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Invalid API key or OTP code",
        ))));
    }

    Logger::sys_info(
        "sre.login",
        "API Key validated successfully. Triggering Vault TOTP verification...",
    );

    // [COMMENT]: 5. Ủy thác xác thực TOTP sang Vault TOTP Secrets Engine — ACL không bao giờ chạm OTP secret
    let is_totp_valid = match token_mgr.verify_admin_totp(&totp_code).await {
        Ok(valid) => valid,
        Err(e) => {
            Logger::sys_error(
                "sre.login",
                "Failed to verify TOTP code with Vault",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to communicate with authentication engine",
            ))));
        }
    };

    if !is_totp_valid {
        Logger::sys_warn(
            "sre.login",
            "SRE Admin login failed: TOTP verification failed",
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Invalid API key or OTP code",
        ))));
    }

    Logger::sys_info(
        "sre.login",
        "SRE Admin TOTP verified successfully. Generating SRE session...",
    );

    // [COMMENT]: 6. Cấp phát Trinity Session SRE (ủy nhiệm sang sre::session::release_sre_session)
    let device_pubkey = payload.device_public_key.as_deref().unwrap_or("");
    let res_val = match release_sre_session(session_mgr, token_mgr, config, device_pubkey).await {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error("sre.login", "Release SRE session failed", &e.to_string());
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                e.message(),
            ))));
        }
    };

    // [COMMENT]: 7. Thiết lập Set-Cookie headers (Path=/admin để tách biệt với user cookies)
    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    // [COMMENT]: HTTP 204 No Content — đăng nhập thành công, không body
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    let access_cookie = format!(
        "access_token={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_token, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    let key_cookie = format!(
        "access_key={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_key, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    let secret_cookie = format!(
        "access_secret={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_secret, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    // [COMMENT]: Set zone_code=global cookie cho SRE Admin (virtual zone)
    let zone_cookie = format!(
        "zone_code=global; Path=/admin; Secure; SameSite=Lax; Max-Age=31536000{}",
        domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("SRE login success"));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "sre.login",
        &format!(
            "SRE Login session registered successfully. access_key={}",
            res_val.access_key
        ),
    );

    Some(Ok(Response::new(response)))
}

// ─── Helper ────────────────────────────────────────────────────────────────────

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let err_resp = ErrorResponse {
        error_message: message.to_string(),
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
