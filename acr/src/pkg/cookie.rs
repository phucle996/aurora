// ======================================================================================================
// 📂 MODULE: acr/src/pkg/cookie.rs
//            Định nghĩa tập trung tên các HTTP Cookie dùng trong hệ thống (Stateless Edge ACR)
// ======================================================================================================

pub const COOKIE_ACCESS_TOKEN: &str = "access_token";
pub const COOKIE_ACCESS_KEY: &str = "access_key";
pub const COOKIE_ACCESS_SECRET: &str = "access_secret";
pub const COOKIE_REFRESH_TOKEN: &str = "refresh_token";
pub const COOKIE_ZONE_CODE: &str = "zone_code";
pub const COOKIE_TENANT_ID: &str = "tenant_id";
pub const COOKIE_TENANT_DOMAIN: &str = "tenant_domain";
pub const COOKIE_WORKSPACE_ID: &str = "workspace_id";
pub const COOKIE_CLIENT_DEVICE_ID: &str = "client_device_id";

/// [COMMENT]: Tạo danh sách các chuỗi header Set-Cookie để xóa sạch toàn bộ cookie nhận được (ngoại trừ client_device_id)
pub fn clear_all_cookies(cookie_header: &str, domain_str: &str, paths: &[&str]) -> Vec<String> {
    let mut cookies_to_clear = std::collections::HashSet::new();

    // Phân tích cú pháp cookie_header để lấy toàn bộ các tên cookie đang tồn tại trên client
    for cookie_part in cookie_header.split(';') {
        let trimmed = cookie_part.trim();
        if trimmed.is_empty() {
            continue;
        }
        if let Some(eq_idx) = trimmed.find('=') {
            let name = trimmed[..eq_idx].trim();
            if name != COOKIE_CLIENT_DEVICE_ID && !name.is_empty() {
                cookies_to_clear.insert(name.to_string());
            }
        }
    }

    // Đảm bảo luôn bao gồm các cookie mặc định của hệ thống phòng trường hợp client không gửi lên
    let default_names = vec![
        COOKIE_ACCESS_TOKEN,
        COOKIE_ACCESS_KEY,
        COOKIE_ACCESS_SECRET,
        COOKIE_REFRESH_TOKEN,
        COOKIE_ZONE_CODE,
        COOKIE_TENANT_ID,
        COOKIE_TENANT_DOMAIN,
        COOKIE_WORKSPACE_ID,
    ];
    for name in default_names {
        cookies_to_clear.insert(name.to_string());
    }

    let mut cookies = Vec::new();
    for name in cookies_to_clear {
        for path in paths {
            let http_only = if name == COOKIE_ACCESS_TOKEN
                || name == COOKIE_ACCESS_KEY
                || name == COOKIE_ACCESS_SECRET
                || name == COOKIE_REFRESH_TOKEN
            {
                " HttpOnly;"
            } else {
                ""
            };
            cookies.push(format!(
                "{}=; Path={};{} Secure; SameSite=Lax; Max-Age=0{}",
                name, path, http_only, domain_str
            ));
        }
    }
    cookies
}
