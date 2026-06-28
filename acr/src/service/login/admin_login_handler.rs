// ======================================================================================================
// 📂 MODULE: acl/src/service/login/admin_login_handler.rs
//            Bộ Điều Hướng Đăng Nhập SRE Admin (SRE Admin Login Controller) Tại Biên (Edge)
// ======================================================================================================

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tonic::{Response, Status};

use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::core::zone::ZoneManager;
use crate::observability::logger::Logger;

// [COMMENT]: Cấu trúc JSON nhận từ client khi SRE Admin đăng nhập
#[derive(Deserialize)]
#[allow(dead_code)]
pub struct AdminLoginPayload {
    pub api_key: Option<String>,
    pub totp_code: Option<String>,
    // [COMMENT]: Khóa công khai Ed25519 của thiết bị/trình duyệt đăng nhập
    pub device_public_key: Option<String>,
}

// [COMMENT]: Cấu trúc JSON phản hồi lỗi nếu xác thực thất bại
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

// [COMMENT]: Hàm xử lý đăng nhập SRE Admin cục bộ tại biên
pub async fn handle_admin_login(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    _zone_mgr: &Arc<ZoneManager>,
    config: &Config,
    _client_headers: &std::collections::HashMap<String, String>,
    req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP POST /admin/auth/login
    if !(method == "POST" && path == "/admin/auth/login") {
        return None;
    }

    Logger::sys_info("SRE-Login", "Intercepted SRE Admin login request at edge");

    // [COMMENT]: Trích xuất Request Body thô dạng byte nhị phân được Envoy gửi sang (hỗ trợ cả text và bytes)
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

    // [COMMENT]: Giải mã JSON payload trực tiếp từ mảng byte (Zero-copy / No intermediate String)
    let payload: AdminLoginPayload = match serde_json::from_slice(&raw_body_bytes) {
        Ok(p) => p,
        Err(e) => {
            Logger::sys_warn(
                "SRE-Login",
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
        "SRE-Login",
        &format!(
            "Attempting SRE verification with TOTP length: {}",
            totp_code.len()
        ),
    );

    // [COMMENT]: Gọi lấy băm API Key (được cache L1 hoặc lấy từ Vault) để thực hiện xác minh
    let admin_api_key_hash = match token_mgr.get_admin_api_key_hash().await {
        Ok(hash) => hash,
        Err(e) => {
            Logger::sys_error(
                "SRE-Login",
                "Failed to retrieve admin api key hash",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to retrieve admin credentials configuration",
            ))));
        }
    };

    // [COMMENT]: Tính băm SHA-256 của input api_key nhận được để đối chiếu bảo mật
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(api_key.as_bytes());
    let input_hash = format!("{:x}", hasher.finalize());

    if input_hash != admin_api_key_hash {
        Logger::sys_warn("SRE-Login", "SRE Admin login failed: API Key mismatch", "");
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Invalid API key or OTP code",
        ))));
    }

    Logger::sys_info(
        "SRE-Login",
        "API Key validated successfully. Triggering Vault TOTP verification...",
    );

    // [COMMENT]: Gửi thẳng totp_code lên Vault để Vault tự xác thực và trả về kết quả
    // Đảm bảo không bao giờ tiếp xúc hay lưu trữ OTP Secret Key tại ACL.
    let is_totp_valid = match token_mgr.verify_admin_totp(&totp_code).await {
        Ok(valid) => valid,
        Err(e) => {
            Logger::sys_error(
                "SRE-Login",
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
            "SRE-Login",
            "SRE Admin login failed: TOTP verification failed",
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Invalid API key or OTP code",
        ))));
    }

    Logger::sys_info(
        "SRE-Login",
        "SRE Admin TOTP verified successfully. Generating SRE session...",
    );

    // [COMMENT]: Sinh Session IDs và Token
    let access_key = uuid::Uuid::new_v4().to_string();
    let access_secret = uuid::Uuid::new_v4().to_string();
    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;

    // [COMMENT]: Khởi tạo Claims cho SRE: Không có role hay lvl phân quyền cụ thể
    // Đăng nhập trực tiếp vào virtual zone "global"
    let claims = crate::core::token::Claims {
        sub: "sre".to_string(),
        uid: "sre".to_string(),
        role: "".to_string(),
        lvl: 0,
        tenant_id: None,
        zone_id: Some("global".to_string()),
        access_key: access_key.clone(),
        jti: uuid::Uuid::new_v4().to_string(),
        iss: Some("aurora-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    // [COMMENT]: Ký sinh JWT qua Vault Transit Engine
    let access_token = match token_mgr.generate_token(&claims).await {
        Ok(t) => t,
        Err(e) => {
            Logger::sys_error(
                "SRE-Login",
                "Vault JWT signing failed for SRE",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue session token",
            ))));
        }
    };

    // [COMMENT]: Băm SHA-256 access_secret
    let ash = sha256_hash(&access_secret);

    // [COMMENT]: Đăng ký Session vào L2 Redis dưới key: "iam:admin_access_session:<access_key>" kèm theo device_public_key
    let device_pubkey = payload.device_public_key.as_deref().unwrap_or("");

    // [COMMENT]: Thực hiện giải mã thử và kiểm tra độ dài public key để phát hiện lỗi sớm trước khi ghi nhận login thành công
    if !device_pubkey.is_empty() {
        use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
        let is_valid = match BASE64.decode(device_pubkey) {
            Ok(bytes) => bytes.len() == 32,
            Err(_) => false,
        };
        if !is_valid {
            Logger::sys_warn(
                "SRE-Login",
                "SRE Admin login failed: Invalid device_public_key format or length",
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Invalid device_public_key format or length (must be a valid 32-byte Base64-encoded key)",
            ))));
        }
    }

    if let Err(e) = session_mgr
        .register_admin_session(&access_key, &ash, device_pubkey)
        .await
    {
        Logger::sys_error(
            "SRE-Login",
            "Redis admin session registration failed",
            &e.to_string(),
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::InternalServerError,
            "Failed to save session state",
        ))));
    }

    // [COMMENT]: Thiết lập Cookie headers
    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent); // Trả về 204 No Content thành công

    // [COMMENT]: Set-Cookie access_token
    let access_cookie = format!(
        "access_token={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        access_token, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Set-Cookie access_key
    let key_cookie = format!(
        "access_key={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        access_key, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    // [COMMENT]: Set-Cookie access_secret
    let secret_cookie = format!(
        "access_secret={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        access_secret, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    // [COMMENT]: Set-Cookie zone_code=global để trình duyệt lưu vết
    let zone_cookie = format!(
        "zone_code=global; Path=/admin; Secure; SameSite=Lax; Max-Age=31536000{}",
        domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("SRE login success"));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "SRE-Login",
        &format!(
            "SRE Login session registered successfully. access_key={}",
            access_key
        ),
    );

    Some(Ok(Response::new(response)))
}

// [COMMENT]: Helper xây dựng denied response chứa body JSON lỗi
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

// [COMMENT]: Helper: Băm SHA-256 mã hóa access_secret
fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
