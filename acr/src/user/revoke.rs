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

// [COMMENT]: Stream consumer gọi hàm typed này để phân biệt payload hỏng (ACK) với
// Auth Redis tạm lỗi (giữ pending để replica ACR khác retry).
pub async fn revoke_sessions_by_devices(
    session_mgr: &Arc<SessionManager>,
    req: &crate::user::device::device_proto::RevokeUserSessionsByDevicesRequest,
) -> Result<i64, String> {
    Logger::sys_info(
        "user.revoke",
        &format!(
            "Revoking sessions for user_id={} and client_device_ids={:?}",
            req.user_id, req.client_device_ids
        ),
    );

    let mut conn = match session_mgr.get_connection().await {
        Ok(c) => c,
        Err(e) => {
            Logger::sys_error(
                "user.revoke",
                "Failed to get Redis connection",
                &e.to_string(),
            );
            return Err(e.to_string());
        }
    };

    let mut revoked_count: i64 = 0;

    for device_id in &req.client_device_ids {
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        let access_keys: Vec<String> = match redis::cmd("SMEMBERS")
            .arg(&dev_index_key)
            .query_async(&mut conn)
            .await
        {
            Ok(keys) => keys,
            Err(e) => {
                Logger::sys_error(
                    "user.revoke",
                    &format!("SMEMBERS failed for device {}", device_id),
                    &e.to_string(),
                );
                // [COMMENT]: Một device lookup lỗi nghĩa là chưa chứng minh toàn bộ target
                // đã được revoke; trả Err để Redis Stream giữ message pending thay vì ACK partial work.
                return Err(e.to_string());
            }
        };

        if access_keys.is_empty() {
            continue;
        }

        // [COMMENT]: Revoke alias trước khi xóa device index. Nếu alias Redis command lỗi,
        // index gốc còn nguyên để redelivery vẫn tìm lại đúng source access keys.
        for session_key in &access_keys {
            if let Some(source_access_key) = session_key.rsplit(':').next() {
                session_mgr
                    .revoke_session_aliases(source_access_key)
                    .await
                    .map_err(|error| error.to_string())?;
            }
        }

        let user_index_key = format!("iam:user_access_index:{}", req.user_id);
        for access_key in &access_keys {
            redis::cmd("EXPIRE")
                .arg(access_key)
                .arg(5)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|error| error.to_string())?;
            redis::cmd("SREM")
                .arg(&user_index_key)
                .arg(access_key)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|error| error.to_string())?;
        }
        redis::cmd("DEL")
            .arg(&dev_index_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                Logger::sys_error(
                    "user.revoke",
                    &format!("Revoke device sessions failed for device {}", device_id),
                    &e.to_string(),
                );
                e.to_string()
            })?;
        revoked_count += access_keys.len() as i64;
    }

    Logger::sys_info(
        "user.revoke",
        &format!(
            "Successfully revoked {} sessions for user_id={}",
            revoked_count, req.user_id
        ),
    );

    Ok(revoked_count)
}

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
