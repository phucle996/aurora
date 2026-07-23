// ======================================================================================================
// 📂 billing/logout.rs — handle_billing_logout: Intercept đăng xuất Billing Domain tại Edge
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::{COOKIE_BILLING_SESSION_ID, COOKIE_BILLING_SESSION_SECRET};

/// [COMMENT]: Intercept POST /api/v1/billing/auth/logout tại Edge.
/// Đặt TTL của session về 5s thay vì xóa trực tiếp để tránh race conditions.
pub async fn handle_billing_logout(
    session_mgr: &Arc<SessionManager>,
    _config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/api/v1/billing/auth/logout") {
        return None;
    }
    if !crate::gateway::csrf::verify_csrf_protection(method, client_headers) {
        return Some(Ok(Response::new(CheckResponse::with_status(
            Status::permission_denied("CSRF validation failed"),
        ))));
    }

    Logger::sys_info(
        "billing.logout",
        "Intercepted billing logout request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let alias_id =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_BILLING_SESSION_ID);
    if let Some(alias_id) = alias_id {
        // [COMMENT]: Chỉ alias đã tồn tại mới cung cấp source key để xóa reverse index đúng family.
        if let Ok(Some(alias)) = session_mgr.get_billing_alias(&alias_id).await {
            let _ = session_mgr
                .delete_billing_alias(&alias_id, &alias.source_access_key)
                .await;
        }
    }

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    // [COMMENT]: Chỉ xóa cookie Billing host-only, không đụng vào IAM cookies cùng browser.
    for cookie in [
        format!("{COOKIE_BILLING_SESSION_ID}=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0"),
        format!(
            "{COOKIE_BILLING_SESSION_SECRET}=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0"
        ),
    ] {
        denied_builder.add_header("set-cookie", &cookie, None, false);
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "Billing logout completed successfully",
    ));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
