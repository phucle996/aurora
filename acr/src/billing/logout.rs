// ======================================================================================================
// 📂 billing/logout.rs — handle_billing_logout: Intercept đăng xuất Billing Auditor tại Edge
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;

/// [COMMENT]: Intercept POST /api/v1/billing/auth/logout tại Edge.
/// Đặt TTL của session về 5s thay vì xóa trực tiếp để tránh race conditions.
pub async fn handle_billing_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/api/v1/billing/auth/logout") {
        return None;
    }

    Logger::sys_info(
        "billing.logout",
        "Intercepted billing logout request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let jwt_token =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN);
    let access_key =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY);

    if let (Some(jwt), Some(key)) = (jwt_token, access_key) {
        // Xác thực token trước khi thay đổi TTL
        if let Ok(_claims) = token_mgr.verify_token(&jwt).await {
            let _ = session_mgr.delete_billing_session(&key).await;
        }
    }

    let domain_str = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    // [COMMENT]: Xóa sạch toàn bộ cookie ngoại trừ client_device_id
    let clear_cookies = clear_all_cookies(&cookie_header, &domain_str, &["/"]);
    for cookie in clear_cookies {
        denied_builder.add_header("set-cookie", &cookie, None, false);
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "Billing logout completed successfully",
    ));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
