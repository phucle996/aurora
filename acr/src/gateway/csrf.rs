// ======================================================================================================
// 📂 gateway/csrf.rs — Cross-Site Request Forgery (CSRF) Protection at Edge
// ======================================================================================================

use std::collections::HashMap;

/// [COMMENT]: Kiểm tra CSRF cho các HTTP state-changing methods (POST, PUT, DELETE, PATCH).
/// Yêu cầu header x-requested-with == "XMLHttpRequest" hoặc Sec-Fetch-Site == "same-origin" / "same-site".
pub fn verify_csrf_protection(
    method: &str,
    headers: &HashMap<String, String>,
) -> bool {
    let method_upper = method.to_uppercase();
    if method_upper == "GET" || method_upper == "HEAD" || method_upper == "OPTIONS" {
        return true;
    }

    if let Some(val) = headers.get("x-requested-with").or_else(|| headers.get("X-Requested-With")) {
        if val.eq_ignore_ascii_case("xmlhttprequest") {
            return true;
        }
    }

    if let Some(val) = headers.get("sec-fetch-site").or_else(|| headers.get("Sec-Fetch-Site")) {
        if val == "same-origin" || val == "same-site" {
            return true;
        }
    }

    // [COMMENT]: Cho phép request nếu có x-admin-signature (SRE critical call)
    if headers.contains_key("x-admin-signature") || headers.contains_key("X-Admin-Signature") {
        return true;
    }

    false
}
