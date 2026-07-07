use crate::config::Config;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;
use crate::pkg::cookie::*;
use std::collections::HashMap;

/// Kiểm tra xem HTTP Method hiện tại có phải là unsafe (POST, PUT, PATCH, DELETE) hay không.
/// Chỉ có unsafe HTTP methods mới có nguy cơ gây thay đổi trạng thái và cần bảo vệ CSRF.
pub fn is_unsafe_method(method: &str) -> bool {
    // Chuyển đổi thành chữ hoa để so khớp case-insensitive
    matches!(
        method.to_uppercase().as_str(),
        "POST" | "PUT" | "PATCH" | "DELETE"
    )
}

/// Kiểm tra xem yêu cầu hiện tại có chứa bất kỳ cookie xác thực nào không.
/// CSRF chỉ khả thi khi trình duyệt tự động gửi các cookie xác thực đính kèm.
pub fn has_auth_cookie(cookie_header: &str) -> bool {
    // Các tên cookie quan trọng định danh session của người dùng hoặc admin
    let keys = [
        COOKIE_ACCESS_TOKEN,
        COOKIE_REFRESH_TOKEN,
        COOKIE_ACCESS_KEY,
        COOKIE_ACCESS_SECRET,
    ];
    // Kiểm tra xem có ít nhất một cookie tồn tại và không trống
    keys.iter()
        .any(|&k| extract_cookie_value(cookie_header, k).is_some())
}

/// Kiểm tra xem yêu cầu hiện tại có sử dụng xác thực dạng Bearer token trong Header hay không.
/// Requests sử dụng explicit Authorization Header thì an toàn trước CSRF vì trình duyệt không tự động gửi.
pub fn is_bearer_auth(headers: &HashMap<String, String>) -> bool {
    if let Some(auth_val) = headers.get("authorization") {
        // Cắt bỏ khoảng trắng và chuyển chữ thường để so sánh chuẩn xác đầu tiền tố
        auth_val.trim().to_lowercase().starts_with("bearer ")
    } else {
        false
    }
}

/// Trích xuất Origin từ Origin header (ưu tiên cao nhất) hoặc Referer header (fallback).
/// Chuỗi trả về luôn được đưa về dạng lowercase để chuẩn hoá việc so sánh.
pub fn extract_origin(headers: &HashMap<String, String>) -> Option<String> {
    // 1. Thử lấy từ header "origin" trực tiếp
    if let Some(origin) = headers.get("origin") {
        return Some(origin.trim().to_lowercase());
    }
    // 2. Fallback sang "referer", cần parse URL để chỉ lấy scheme://host
    if let Some(referer) = headers.get("referer") {
        if let Ok(u) = url::Url::parse(referer) {
            let scheme = u.scheme();
            if let Some(host) = u.host_str() {
                // Định dạng chuẩn hóa: scheme://host
                if let Some(port) = u.port() {
                    return Some(format!("{}://{}:{}", scheme, host, port));
                } else {
                    return Some(format!("{}://{}", scheme, host));
                }
            }
        }
    }
    None
}

/// Xác thực tính hợp lệ của Origin/Referer tìm thấy.
/// Phải nằm trong danh sách whitelist (allowed_origins) hoặc trùng khớp với Host/Authority của chính server.
pub fn is_allowed_origin(origin: &str, allowed: &[String], authority: &str) -> bool {
    // 1. So khớp với whitelist được cấu hình động từ biến môi trường
    if allowed
        .iter()
        .any(|allowed_org| allowed_org.to_lowercase() == origin)
    {
        return true;
    }

    // 2. Same-origin fallback check: So khớp host của Origin với authority/host của yêu cầu
    if let Ok(u) = url::Url::parse(origin) {
        if let Some(host) = u.host_str() {
            // Tách port khỏi authority (nếu có) để so sánh host thuần túy
            let auth_host = authority.split(':').next().unwrap_or("").trim();
            return host.eq_ignore_ascii_case(auth_host);
        }
    }
    false
}

/// Hàm điều phối chính thực hiện kiểm tra an toàn CSRF.
/// Trả về `Ok(())` nếu an toàn hoặc được miễn trừ; trả về `Err(msg)` nếu phát hiện vi phạm.
pub fn verify_csrf(
    method: &str,
    path: &str,
    cookie_header: &str,
    headers: &HashMap<String, String>,
    config: &Config,
) -> Result<(), String> {
    // Thực hiện bảo vệ khi:
    // - HTTP Method không an toàn (POST/PUT/PATCH/DELETE)
    // - Yêu cầu đính kèm cookie xác thực
    // - Yêu cầu không sử dụng explicit Bearer Token
    if is_unsafe_method(method) && has_auth_cookie(cookie_header) && !is_bearer_auth(headers) {
        if let Some(origin) = extract_origin(headers) {
            // Lấy Host hoặc :authority được gửi lên từ Envoy Gateway
            let authority = headers
                .get(":authority")
                .or_else(|| headers.get("host"))
                .map(|s| s.as_str())
                .unwrap_or("")
                .trim();

            // Thực hiện so khớp kiểm định Origin
            if !is_allowed_origin(&origin, &config.allowed_origins, authority) {
                Logger::authz_log(
                    "anonymous",
                    method,
                    path,
                    "DENIED",
                    &format!("CSRF check failed: origin {} not allowed", origin),
                );
                return Err("CSRF verification failed: origin not allowed".to_string());
            }
        } else {
            // Bị phát hiện dùng cookie auth trên unsafe route mà không có Origin hay Referer header -> Suspicious request
            Logger::authz_log(
                "anonymous",
                method,
                path,
                "DENIED",
                "CSRF check failed: missing origin/referer for cookie-auth unsafe request",
            );
            return Err(
                "Forbidden: Missing Origin or Referer header for cookie-auth request".to_string(),
            );
        }
    }
    Ok(())
}
