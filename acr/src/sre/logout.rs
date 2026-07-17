// ======================================================================================================
// 📂 sre/logout.rs — handle_sre_logout: Intercept đăng xuất SRE tại Edge
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

/// [COMMENT]: Intercept POST /admin/auth/logout tại Edge.
/// Thu hồi session SRE bằng cách giảm TTL về 5s (Grace Period) và dọn cookies.
pub async fn handle_sre_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
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
            if claims.is_admin() {
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

    // Xóa cookies với Path=/admin
    let access_cookie = format!(
        "access_token=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=0{}",
        domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    let key_cookie = format!(
        "access_key=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=0{}",
        domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    let secret_cookie = format!(
        "access_secret=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=0{}",
        domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("SRE logout completed successfully"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
