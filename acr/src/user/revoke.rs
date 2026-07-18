// ======================================================================================================
// 📂 user/revoke.rs — Handle logout/revoke session (User & SRE) & Revoke sessions by device
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

/// [COMMENT]: Giải mã yêu cầu từ bytes, thực thi quét Redis L2 để thu hồi session thuộc thiết bị được chọn
pub async fn revoke_sessions_bytes(session_mgr: &Arc<SessionManager>, payload: &[u8]) -> Vec<u8> {
    use crate::user::device::device_proto::{
        RevokeUserSessionsByDevicesRequest, RevokeUserSessionsByDevicesResponse,
    };
    use prost::Message;

    let req = match RevokeUserSessionsByDevicesRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error(
                "user.revoke",
                "Failed to decode RevokeUserSessionsByDevicesRequest",
                &e.to_string(),
            );
            return vec![];
        }
    };

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
            return vec![];
        }
    };

    let mut revoked_count = 0;

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
                continue;
            }
        };

        if access_keys.is_empty() {
            continue;
        }

        let user_index_key = format!("iam:user_access_index:{}", req.user_id);
        let mut pipe = redis::pipe();
        pipe.atomic();

        for access_key in &access_keys {
            pipe.cmd("EXPIRE").arg(access_key).arg(5);
            pipe.cmd("SREM").arg(&user_index_key).arg(access_key);
            revoked_count += 1;
        }

        pipe.cmd("DEL").arg(&dev_index_key);

        if let Err(e) = pipe.query_async::<_, ()>(&mut conn).await {
            Logger::sys_error(
                "user.revoke",
                &format!(
                    "Revoke device session pipeline failed for device {}",
                    device_id
                ),
                &e.to_string(),
            );
        }
    }

    Logger::sys_info(
        "user.revoke",
        &format!(
            "Successfully revoked {} sessions for user_id={}",
            revoked_count, req.user_id
        ),
    );

    let res = RevokeUserSessionsByDevicesResponse { revoked_count };
    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        vec![]
    }
}

/// [COMMENT]: Intercept POST /api/v1/auth/logout tại Edge cho User thường
pub async fn handle_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
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
