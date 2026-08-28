// ======================================================================================================
// 📂 user/revoke.rs — Handle logout/revoke session (User & SRE) & Revoke sessions by device
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::token::TokenManager;
use prost::Message;



/// [COMMENT]: Intercept POST /api/v1/auth/logout tại Edge cho User thường
pub async fn handle_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "POST" && path == "/api/v1/auth/logout") {
        return None;
    }

    Logger::sys_info("user.revoke", "Intercepted user logout request at edge");

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let jwt_token =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN);
    let access_key =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY);

    if let Some(refresh_token) =
        crate::gateway::ext_authz::extract_cookie_value(&cookie_header, COOKIE_REFRESH_TOKEN)
    {
        if !(64..=512).contains(&refresh_token.len()) {
            let mut denied = DeniedHttpResponseBuilder::new();
            denied.set_http_status(HttpStatusCode::Unauthorized);
            let mut response = CheckResponse::new();
            response.set_status(Status::unauthenticated("Invalid refresh credential"));
            response.set_http_response(denied);
            return Some(Ok(Response::new(response)));
        }

        let request =
            crate::infra::iam_proto::auth::RevokeOpaqueRefreshTokenRequest { refresh_token };
        let mut request_bytes = Vec::new();
        let revoked = request.encode(&mut request_bytes).is_ok()
            && shared_redis
                .request(
                    "iam.auth.revoke_opaque_token",
                    "iam.auth.revoke_opaque_token.reply.",
                    request_bytes,
                    std::time::Duration::from_millis(800),
                )
                .await
                .ok()
                .and_then(|payload| {
                    crate::infra::iam_proto::auth::RevokeOpaqueRefreshTokenResponse::decode(
                        payload.as_slice(),
                    )
                    .ok()
                })
                .is_some();
        if !revoked {
            // Durable credential revocation precedes runtime cleanup. Clearing
            // cookies first would strand a still-valid stolen refresh token and
            // remove the user's ability to retry the logout safely.
            let mut denied = DeniedHttpResponseBuilder::new();
            denied.set_http_status(HttpStatusCode::ServiceUnavailable);
            let mut response = CheckResponse::new();
            response.set_status(Status::unavailable("Logout temporarily unavailable"));
            response.set_http_response(denied);
            return Some(Ok(Response::new(response)));
        }
    }

    if let (Some(jwt), Some(key)) = (jwt_token, access_key) {
        if let Ok(claims) = token_mgr.verify_token(&jwt).await {
            let _ = session_mgr
                .delete_session(
                    claims.zone_id.as_deref().unwrap_or("global"),
                    claims.tenant_id.as_deref().unwrap_or("platform"),
                    &claims.uid,
                    &key,
                )
                .await;
            let _ = session_mgr.revoke_session_aliases(&key).await;
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
    response.set_status(Status::unauthenticated("Logout completed successfully"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
