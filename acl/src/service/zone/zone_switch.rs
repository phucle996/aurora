// ======================================================================================================
// 📂 MODULE: acl/src/service/zone_switch.rs
//            BỘ ĐIỀU HƯỚNG CHUYỂN NGỮ CẢNH ZONE (ZONE SWITCH CONTROLLER) TẠI BIÊN (EDGE)
// ======================================================================================================

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::core::zone::ZoneManager;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;

// [COMMENT]: Cấu trúc JSON trả về khi chuyển Active Zone thành công
#[derive(Serialize)]
pub struct ZoneSwitchSuccessResponse {
    pub zone_code: String,
}

// [COMMENT]: Cấu trúc JSON thông báo lỗi chung
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

// [COMMENT]: Hàm phân tích cú pháp an toàn lấy zone_code từ query parameters của URL (:path)
fn parse_zone_code(path: &str) -> Option<String> {
    // [COMMENT]: Tìm kiếm ký tự phân tách tham số "?" trong chuỗi đường dẫn
    path.find('?').and_then(|pos| {
        let query_str = &path[pos + 1..];
        // [COMMENT]: Duyệt qua các cặp key=value trong query string
        query_str
            .split('&')
            .find(|pair| pair.starts_with("zone_code="))
            .map(|pair| pair["zone_code=".len()..].to_string())
    })
}

// [COMMENT]: Helper xây dựng denied response chứa body JSON lỗi với thông điệp chung
fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let err_resp = ErrorResponse {
        error_message: message.to_string(),
    };
    // [COMMENT]: Chèn tiền tố bảo mật chống CSRF XSSI theo đúng tiêu chuẩn
    let json_body = format!(
        ")]}}',\n{}",
        serde_json::to_string(&err_resp).unwrap_or_default()
    );

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

// [COMMENT]: Hàm xử lý chuyển Active Zone tường minh tại biên (Edge Ingress)
pub async fn handle_zone_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<ZoneManager>,
    config: &Config,
    client_headers: &std::collections::HashMap<String, String>,
    _req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP POST /api/v1/zone/go-to-zone
    if !(method == "POST" && path.starts_with("/api/v1/zone/go-to-zone")) {
        return None;
    }

    Logger::sys_info(
        "zone_switch",
        &format!("Intercepted zone switch request at edge: {}", path),
    );

    // [COMMENT]: Trích xuất và định dạng zone_code nhận về từ query param
    let zone_code = match parse_zone_code(path) {
        Some(code) if !code.trim().is_empty() => code.trim().to_lowercase(),
        _ => {
            Logger::sys_warn(
                "zone_switch",
                "Missing or empty zone_code in query parameters",
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Trích xuất cookie từ HTTP header để xác thực Trinity Credentials
    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    let auth_result = async {
        // [COMMENT]: 1. Lấy access_token JWT từ cookie
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")
            .ok_or("Missing access_token cookie")?;

        // [COMMENT]: 2. Lấy access_key dùng để truy vấn session
        let access_key = extract_cookie_value(&cookie_header, "access_key")
            .ok_or("Missing access_key cookie")?;

        // [COMMENT]: 3. Giải mã và verify JWT Token qua Vault
        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("zone_switch", &format!("JWT verification failed: {}", e));
            "Invalid access_token"
        })?;

        // [COMMENT]: 4. So khớp access_key giữa token và cookie chống Replay Attack
        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        // [COMMENT]: 5. Lấy session hiện tại từ Redis L2
        let session = match session_mgr.get_session(&claims.sub, &access_key).await {
            Ok(Some(s)) => s,
            Ok(None) => return Err("Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error(
                    "zone_switch",
                    "Redis error while validating session",
                    &e.to_string(),
                );
                return Err("Authentication service unavailable");
            }
        };

        // [COMMENT]: 6. Trích xuất access_secret từ cookie HttpOnly
        let access_secret = extract_cookie_value(&cookie_header, "access_secret")
            .ok_or("Missing access_secret cookie")?;

        // [COMMENT]: 7. Kiểm tra đối chiếu hash SHA-256 của access_secret với session.ash trong Redis L2
        let incoming_hash = sha256_hash(&access_secret);
        if session.ash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok((claims, session, access_key))
    }
    .await;

    // [COMMENT]: Tách biệt việc xử lý lỗi xác thực
    let (mut claims, _session, _access_key) = match auth_result {
        Ok(val) => val,
        Err(err_msg) => {
            Logger::sys_warn(
                "zone_switch",
                &format!("Authentication failed for zone switch: {}", err_msg),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    // [COMMENT]: Tra cứu phân giải zone_code sang zone_id bằng L1 Cache cục bộ
    let zone_res = zone_mgr.resolve_code_to_id_and_status(&zone_code).await;
    let (resolved_zone_id, resolved_zone_status) = match zone_res {
        Some(res) => res,
        None => {
            // [COMMENT]: Log chi tiết lỗi để điều tra hệ thống
            Logger::sys_warn(
                "zone_switch",
                &format!("Zone code not found in cache: {}", zone_code),
                "",
            );
            // [COMMENT]: Trả lỗi chung chung "Zone unavailable" về client
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Kiểm tra các ràng buộc bảo mật của Zone
    if !claims.is_admin() {
        // [COMMENT]: Ràng buộc 1: Chặn user thường vào zone global (UUID rỗng)
        if zone_code == "global" || resolved_zone_id == "00000000-0000-0000-0000-000000000000" {
            Logger::sys_warn(
                "zone_switch",
                &format!(
                    "Non-admin user {} attempted to access global zone",
                    claims.sub
                ),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Forbidden,
                "Zone unavailable",
            ))));
        }

        // [COMMENT]: Ràng buộc 2: Chặn user thường vào zone không hoạt động (status != active)
        if resolved_zone_status != "active" {
            Logger::sys_warn(
                "zone_switch",
                &format!(
                    "Non-admin user {} attempted to access inactive zone: {} is {}",
                    claims.sub, zone_code, resolved_zone_status
                ),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Forbidden,
                "Zone unavailable",
            ))));
        }
    }

    // [COMMENT]: Cập nhật zone_id mới vào claims
    claims.zone_id = Some(resolved_zone_id.clone());

    // [COMMENT]: Ký lại token JWT mới chứa zone_id cập nhật qua Vault Transit Engine
    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error(
                "zone_switch",
                "Failed to generate access token with updated zone_id",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Trích xuất cookie domain từ file cấu hình
    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    // [COMMENT]: Tạo payload trả về cho client (không chứa zone_id theo yêu cầu)
    let success_payload = ZoneSwitchSuccessResponse {
        zone_code: zone_code.clone(),
    };
    let json_body = format!(
        ")]}}',\n{}",
        serde_json::to_string(&success_payload).unwrap_or_default()
    );

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    // [COMMENT]: Cập nhật cookie access_token (HttpOnly)
    let access_cookie = format!(
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        new_jwt, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Cập nhật cookie zone_code (không HttpOnly để Client JS đọc trực tiếp)
    let zone_cookie = format!(
        "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        zone_code, domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    // [COMMENT]: Trả về trạng thái unauthenticated cùng DeniedHttpResponse để Envoy Edge trực tiếp trả cookies/body về client
    response.set_status(Status::unauthenticated(
        "Zone switch completed successfully",
    ));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "zone_switch",
        &format!(
            "Successfully switched zone to {} for user_id={}",
            zone_code, claims.sub
        ),
    );

    Some(Ok(Response::new(response)))
}

// [COMMENT]: Hàm xử lý chuyển Active Zone cho SRE Admin tại biên (POST /admin/zone/go-to-zone)
pub async fn handle_admin_zone_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<ZoneManager>,
    config: &Config,
    client_headers: &std::collections::HashMap<String, String>,
    _req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP POST /admin/zone/go-to-zone
    if !(method == "POST" && path.starts_with("/admin/zone/go-to-zone")) {
        return None;
    }

    Logger::sys_info(
        "admin_zone_switch",
        &format!("Intercepted admin zone switch request at edge: {}", path),
    );

    // [COMMENT]: Trích xuất và định dạng zone_code nhận về từ query param
    let zone_code = match parse_zone_code(path) {
        Some(code) if !code.trim().is_empty() => code.trim().to_lowercase(),
        _ => {
            Logger::sys_warn(
                "admin_zone_switch",
                "Missing or empty zone_code in query parameters",
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Trích xuất cookie từ HTTP header để xác thực Trinity Credentials
    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    let auth_result = async {
        // [COMMENT]: 1. Lấy access_token JWT từ cookie
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")
            .ok_or("Missing access_token cookie")?;

        // [COMMENT]: 2. Lấy access_key dùng để truy vấn session
        let access_key = extract_cookie_value(&cookie_header, "access_key")
            .ok_or("Missing access_key cookie")?;

        // [COMMENT]: 3. Giải mã và verify JWT Token qua Vault
        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug(
                "admin_zone_switch",
                &format!("JWT verification failed: {}", e),
            );
            "Invalid access_token"
        })?;

        // [COMMENT]: 4. Kiểm tra vai trò Admin (SRE)
        if !claims.is_admin() {
            return Err("Forbidden: Not an admin");
        }

        // [COMMENT]: 5. So khớp access_key giữa token và cookie chống Replay Attack
        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        // [COMMENT]: 6. Lấy session admin hiện tại từ Redis L2 (Bỏ zone_id khỏi key theo kiến trúc tĩnh HA)
        let session = match session_mgr.get_admin_session(&access_key).await {
            Ok(Some(s)) => s,
            Ok(None) => return Err("Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error(
                    "admin_zone_switch",
                    "Redis error while validating admin session",
                    &e.to_string(),
                );
                return Err("Authentication service unavailable");
            }
        };

        // [COMMENT]: 7. Trích xuất access_secret từ cookie HttpOnly
        let access_secret = extract_cookie_value(&cookie_header, "access_secret")
            .ok_or("Missing access_secret cookie")?;

        // [COMMENT]: 8. Kiểm tra đối chiếu hash SHA-256 của access_secret với session.ash trong Redis L2
        let incoming_hash = sha256_hash(&access_secret);
        if session.access_secret_hash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok((claims, access_key))
    }
    .await;

    // [COMMENT]: Tách biệt việc xử lý lỗi xác thực
    let (mut claims, _access_key) = match auth_result {
        Ok(val) => val,
        Err(err_msg) => {
            Logger::sys_warn(
                "admin_zone_switch",
                &format!("Authentication failed for admin zone switch: {}", err_msg),
                "",
            );
            let status_code = if err_msg.contains("Forbidden") {
                HttpStatusCode::Forbidden
            } else {
                HttpStatusCode::Unauthorized
            };
            return Some(Ok(Response::new(build_denied_json(
                status_code,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Phân giải zone_code sang zone_id bằng L1 Cache cục bộ hoặc local mapping cho global
    let resolved_zone_id = if zone_code == "global" {
        "global".to_string()
    } else {
        match zone_mgr.resolve_code_to_id_and_status(&zone_code).await {
            Some((zone_id, _status)) => zone_id,
            None => {
                Logger::sys_warn(
                    "admin_zone_switch",
                    &format!("Zone code not found in cache for admin: {}", zone_code),
                    "",
                );
                return Some(Ok(Response::new(build_denied_json(
                    HttpStatusCode::BadRequest,
                    "Zone unavailable",
                ))));
            }
        }
    };

    // [COMMENT]: Cập nhật zone_id mới vào claims
    claims.zone_id = Some(resolved_zone_id.clone());

    // [COMMENT]: Ký lại token JWT mới chứa zone_id cập nhật qua Vault Transit Engine
    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error(
                "admin_zone_switch",
                "Failed to generate access token with updated zone_id for admin",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Zone unavailable",
            ))));
        }
    };

    // [COMMENT]: Trích xuất cookie domain từ file cấu hình
    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    // [COMMENT]: Tạo payload trả về cho client (không chứa zone_id theo yêu cầu)
    let success_payload = ZoneSwitchSuccessResponse {
        zone_code: zone_code.clone(),
    };
    let json_body = format!(
        ")]}}',\n{}",
        serde_json::to_string(&success_payload).unwrap_or_default()
    );

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    // [COMMENT]: Cập nhật cookie access_token (HttpOnly)
    let access_cookie = format!(
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        new_jwt, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Cập nhật cookie zone_code (không HttpOnly để Client JS đọc trực tiếp)
    let zone_cookie = format!(
        "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        zone_code, domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    // [COMMENT]: Trả về trạng thái unauthenticated cùng DeniedHttpResponse để Envoy Edge trực tiếp trả cookies/body về client
    response.set_status(Status::unauthenticated(
        "Zone switch completed successfully",
    ));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "admin_zone_switch",
        &format!(
            "Successfully switched zone to {} for admin_id={}",
            zone_code, claims.sub
        ),
    );

    Some(Ok(Response::new(response)))
}
