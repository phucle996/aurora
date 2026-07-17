// ======================================================================================================
// 📂 sre/logout.rs — handle_sre_logout: Intercept đăng xuất SRE tại Edge
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
use crate::pkg::cookie::*;
use crate::sre::claims::SreTokenManager;

/// [COMMENT]: Intercept POST /admin/auth/logout tại Edge.
/// Thu hồi session SRE bằng cách giảm TTL về 5s (Grace Period) và dọn cookies.
pub async fn handle_sre_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<SreTokenManager>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/admin/auth/logout") {
        return None;
    }

    Logger::sys_info("sre.logout", "Intercepted SRE logout request at edge");

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let jwt_token =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN);
    let access_key =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY);

    if let (Some(jwt), Some(key)) = (jwt_token, access_key) {
        if let Ok(claims) = token_mgr.verify_token(&jwt).await {
            if claims.sub == "sre" {
                let _ = session_mgr.delete_sre_session(&key).await;
            }
        }
    }

    let domain_str = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    // [COMMENT]: Xóa sạch toàn bộ cookie ngoại trừ client_device_id trên cả path / và /admin
    let clear_cookies = clear_all_cookies(&cookie_header, &domain_str, &["/", "/admin"]);
    for cookie in clear_cookies {
        denied_builder.add_header("set-cookie", &cookie, None, false);
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("SRE logout completed successfully"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
