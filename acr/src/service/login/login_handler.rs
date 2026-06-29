// ======================================================================================================
// 📂 MODULE: acl/src/service/login_handler.rs
//            Bộ Điều Hướng Đăng Nhập (Login Controller) Tại Biên (Edge) Theo Option 2: Ext-Authz as Controller
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
use crate::infra::controlplane::auth::VerifyUserCredentialsRequest;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;

// [COMMENT]: Cấu trúc JSON nhận từ client khi đăng nhập
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

// [COMMENT]: Cấu trúc JSON phản hồi lỗi nếu xác thực thất bại
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

// [COMMENT]: Hàm xử lý đăng nhập cục bộ tại biên
pub async fn handle_login(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    control_plane_client: &Arc<ControlPlaneClient>,
    zone_mgr: &Arc<ZoneManager>,
    config: &Config,
    client_headers: &std::collections::HashMap<String, String>,
    req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP POST /api/v1/auth/login
    if !(method == "POST" && path == "/api/v1/auth/login") {
        return None;
    }

    Logger::sys_info("login_handler", "Intercepted login request at edge");

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
    let payload: LoginPayload = match serde_json::from_slice(&raw_body_bytes) {
        Ok(p) => p,
        Err(e) => {
            Logger::sys_warn(
                "login_handler",
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

    // [COMMENT]: Trích xuất client_device_id từ cookie (nếu có)
    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let client_device_id =
        extract_cookie_value(&cookie_header, "client_device_id").unwrap_or_default();

    // [COMMENT]: Lấy địa chỉ IP và User Agent phục vụ lưu vết phiên
    let client_ip = client_headers
        .get("x-forwarded-for")
        .cloned()
        .unwrap_or_else(|| "unknown".to_string());
    let user_agent = client_headers
        .get("user-agent")
        .cloned()
        .unwrap_or_else(|| "unknown".to_string());

    // [COMMENT]: Gọi gRPC VerifyUserCredentials sang Control Plane (Go) để kiểm chứng mật khẩu & trạng thái
    let cp_req = VerifyUserCredentialsRequest {
        username,
        password,
        client_device_id,
        device_name: payload.device_name.unwrap_or_default(),
        device_type: payload.device_type.unwrap_or_default(),
        public_key: payload.public_key.unwrap_or_default(),
        signature: payload.signature.unwrap_or_default(),
        trust_device: payload.trust_device.unwrap_or(false),
        client_ip,
        user_agent,
    };

    Logger::sys_info(
        "login_handler",
        &format!(
            "Forwarding verify request to Control Plane for user={}",
            cp_req.username
        ),
    );

    let cp_res = match control_plane_client.verify_user_credentials(cp_req).await {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "login_handler",
                "gRPC VerifyUserCredentials to CP failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service is temporarily unavailable",
            ))));
        }
    };

    // [COMMENT]: Nếu CP báo xác thực không hợp lệ (sai mật khẩu, chưa kích hoạt, bị khóa, etc)
    if !cp_res.valid {
        let err_msg = if cp_res.error_message.is_empty() {
            "Invalid username or password".to_string()
        } else {
            cp_res.error_message
        };
        Logger::sys_warn(
            "login_handler",
            &format!("Authentication rejected by CP: {}", err_msg),
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            &err_msg,
        ))));
    }

    // [COMMENT]: CẤP PHÁT TRINITY SESSION (Ủy nhiệm sang release_session.rs)
    let zone_code = payload.zone_code.as_deref().unwrap_or("global");
    let res_val = match crate::service::session::release_session::release_user_session(
        session_mgr,
        token_mgr,
        zone_mgr,
        config,
        &cp_res.user_id,
        &cp_res.username,
        &cp_res.role,
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
            Logger::sys_error(
                "login_handler",
                "Release user session failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue session state",
            ))));
        }
    };

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
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_token, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Set-Cookie access_key
    let key_cookie = format!(
        "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_key, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    // [COMMENT]: Set-Cookie access_secret
    let secret_cookie = format!(
        "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_secret, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    // [COMMENT]: Set-Cookie client_device_id
    let cdid_cookie = format!(
        "client_device_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        res_val.client_device_id, domain_str
    );
    denied_builder.add_header("set-cookie", &cdid_cookie, None, false);

    // [COMMENT]: Set-Cookie zone_code (nếu có để client lưu vết)
    if let Some(ref zone_code) = payload.zone_code {
        let zone_cookie = format!(
            "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            zone_code, domain_str
        );
        denied_builder.add_header("set-cookie", &zone_cookie, None, false);
    }

    // [COMMENT]: Set-Cookie tenant_id (chứa tnc hiện tại, mặc định là global)
    let tenant_cookie = format!(
        "tenant_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        res_val.tenant_id_val, domain_str
    );
    denied_builder.add_header("set-cookie", &tenant_cookie, None, false);

    // [COMMENT]: Set-Cookie refresh_token (nếu CP cấp mới)
    if !cp_res.refresh_token.is_empty() {
        let refresh_cookie = format!(
            "refresh_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=2592000{}",
            cp_res.refresh_token, domain_str
        );
        denied_builder.add_header("set-cookie", &refresh_cookie, None, false);
    }

    // [COMMENT]: Trả thêm header X-Client-Device-Id cho Envoy
    denied_builder.add_header("x-client-device-id", &res_val.client_device_id, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Local intercept login success"));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "login_handler",
        &format!(
            "Login session released successfully for user_id={}",
            cp_res.user_id
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
