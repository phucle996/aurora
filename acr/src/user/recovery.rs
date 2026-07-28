// ======================================================================================================
// 📂 user/recovery.rs — Phục hồi session khi JWT hết hạn (Token Recovery)
// ======================================================================================================

use crate::config::Config;
use crate::error::AcrError;
use crate::infra::redis::{RecoverySessionCache, SessionManager};
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::pkg::header::*;
use crate::token::TokenManager;
use crate::user::login::release_user_session;
use crate::user::zone_resolution::resolve_zone_context;
use std::collections::HashMap;
use std::sync::Arc;

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use tonic::{Response, Status};

async fn release_recovery_lock(session_mgr: &SessionManager, token_hash: &str) {
    if let Err(e) = session_mgr.release_recovery_lock(token_hash).await {
        Logger::sys_error(
            "user.recovery",
            "Failed to release recovery lock",
            &e.to_string(),
        );
    }
}

fn decode_jwt_claims_unsafe(jwt_token: &str) -> Option<(String, Option<String>)> {
    let parts: Vec<&str> = jwt_token.split('.').collect();
    if parts.len() < 2 {
        return None;
    }
    let payload_b64 = parts[1];

    use base64::Engine;
    let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
    let payload_bytes = url_engine.decode(payload_b64).ok()?;
    let payload_val: serde_json::Value = serde_json::from_slice(&payload_bytes).ok()?;

    let uid = payload_val.get("uid")?.as_str()?.to_string();
    let tenant_id = payload_val
        .get("tenant_id")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());

    Some((uid, tenant_id))
}

fn build_success_response(
    user_id: &str,
    role_id: &str,
    level: i32,
    tenant_id: &str,
    new_jwt: &str,
    new_access_key: &str,
    new_access_secret: &str,
    cookie_header: &str,
    zone_id: &str,
    zone_code: &str,
) -> Result<Response<CheckResponse>, Status> {
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(")]}}',\n{\"status\":\"ok\"}");

    let domain_str = ""; // recovery is domain-neutral or set dynamic if needed

    builder.add_header(
        "set-cookie",
        &format!(
            "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=7200{}",
            new_jwt, domain_str
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=7200{}",
            new_access_key, domain_str
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=7200{}",
            new_access_secret, domain_str
        ),
        None,
        false,
    );

    let client_device_id =
        crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID);
    if let Some(dev_id) = client_device_id {
        builder.add_header(
            "set-cookie",
            &format!(
                "client_device_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
                dev_id, domain_str
            ),
            None,
            false,
        );
    }

    builder.add_header(
        "set-cookie",
        &format!(
            "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            zone_code, domain_str
        ),
        None,
        false,
    );

    let mut response = CheckResponse::new();
    response.set_status(Status::ok("authorized_via_recovery_sliding"));
    response.set_http_response(builder);

    // Set inject headers
    if let Some(ref mut http_resp) = response.http_response {
        use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
        if let HttpResponse::DeniedResponse(ref mut denied) = http_resp {
            use envoy_types::pb::envoy::config::core::v3::HeaderValueOption;
            let headers = vec![
                (HEADER_X_USER_ID, user_id.to_string()),
                (HEADER_X_USER_ROLE_ID, role_id.to_string()),
                (HEADER_X_USER_LEVEL, level.to_string()),
                (HEADER_X_TENANT_ID, tenant_id.to_string()),
                (HEADER_X_ZONE_ID, zone_id.to_string()),
            ];
            for (k, v) in headers {
                let mut h = HeaderValueOption::default();
                h.header = Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                    key: k.to_string(),
                    value: v,
                });
                denied.headers.push(h);
            }
        }
    }

    Ok(Response::new(response))
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let err_resp = crate::user::login::ErrorResponse {
        error_message: message.to_string(),
        error_code: None,
        verification_email_queued: None,
        mfa_required: None,
        challenge_id: None,
        expires_in: None,
        methods: None,
    };
    let json_body = serde_json::to_string(&err_resp).unwrap_or_default();

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(status);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(denied_builder);
    response
}

/// [COMMENT]: Phục hồi session đã hết hạn nhưng access_secret còn hợp lệ (Sliding window)
pub async fn try_handle_recovery_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    redis_client: &redis::Client,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    cookie_header: &str,
    jwt_token: &str,
    _access_key: &str,
    _client_headers: &HashMap<String, String>,
) -> Option<Result<Response<CheckResponse>, Status>> {
    let refresh_token =
        crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_REFRESH_TOKEN)?;
    let token_hash = crate::gateway::ext_authz::sha256_hash(&refresh_token);

    if let Ok(Some(cache)) = session_mgr.get_recovery_cache(&token_hash).await {
        return Some(build_success_response(
            &cache.user_id,
            &cache.role_id,
            cache.level,
            &cache.tenant_id,
            &cache.new_jwt,
            &cache.new_access_key,
            &cache.new_access_secret,
            cookie_header,
            &cache.zone_id,
            &cache.zone_code,
        ));
    }

    if let Ok(true) = session_mgr.is_recovery_locked(&token_hash).await {
        for _ in 0..15 {
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
            if let Ok(Some(cache)) = session_mgr.get_recovery_cache(&token_hash).await {
                return Some(build_success_response(
                    &cache.user_id,
                    &cache.role_id,
                    cache.level,
                    &cache.tenant_id,
                    &cache.new_jwt,
                    &cache.new_access_key,
                    &cache.new_access_secret,
                    cookie_header,
                    &cache.zone_id,
                    &cache.zone_code,
                ));
            }
        }
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Recovery lock timeout",
        ))));
    }

    let acquired = match session_mgr.try_lock_recovery(&token_hash).await {
        Ok(true) => true,
        _ => false,
    };

    if !acquired {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Recovery lock collision",
        ))));
    }

    let (user_id, tenant_id_opt) = match decode_jwt_claims_unsafe(jwt_token) {
        Some(res) => res,
        None => {
            release_recovery_lock(session_mgr, &token_hash).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Invalid token payload format for recovery",
            ))));
        }
    };

    let tenant_id = tenant_id_opt.unwrap_or_else(|| "platform".to_string());

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) = match resolve_zone_context(
        shared_redis,
        redis_client,
        cookie_header,
        &HashMap::new(),
    )
    .await
    {
        Ok(res) => res,
        Err(_) => {
            release_recovery_lock(session_mgr, &token_hash).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "A concrete zone_code is required",
            ))));
        }
    };
    if resolved_zone_code == "global"
        || resolved_zone_id == "00000000-0000-0000-0000-000000000000"
        || (resolved_zone_status != "active" && resolved_zone_status != "draining")
    {
        release_recovery_lock(session_mgr, &token_hash).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Zone unavailable",
        ))));
    }

    let cp_req = crate::infra::iam_proto::auth::VerifyOpaqueRefreshTokenRequest {
        refresh_token: refresh_token.clone(),
        tenant_id: Some(tenant_id.clone()),
        user_id: user_id.clone(),
    };

    let mut payload_bytes = Vec::new();
    if cp_req.encode(&mut payload_bytes).is_err() {
        release_recovery_lock(session_mgr, &token_hash).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::InternalServerError,
            "Failed to serialize recovery request",
        ))));
    }

    // [COMMENT]: Refresh verification là request đồng bộ Central nội bộ; Shared Redis
    // request_id bảo đảm một CP replica xử lý và recovery lock bảo đảm một ACR rotation.
    let response_payload = match shared_redis
        .request(
            "iam.auth.verify_opaque_token",
            "iam.auth.verify_opaque_token.reply.",
            payload_bytes,
            std::time::Duration::from_secs(10),
        )
        .await
    {
        Ok(payload) => payload,
        Err(e) => {
            Logger::sys_error(
                "user.recovery",
                "Shared Redis opaque token request failed",
                &e,
            );
            release_recovery_lock(session_mgr, &token_hash).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Auth backend temporarily unavailable",
            ))));
        }
    };

    let cp_res = match crate::infra::iam_proto::auth::VerifyOpaqueRefreshTokenResponse::decode(
        response_payload.as_slice(),
    ) {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "user.recovery",
                "Shared Redis opaque token response decode failed",
                &e.to_string(),
            );
            release_recovery_lock(session_mgr, &token_hash).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Auth backend response format error",
            ))));
        }
    };

    if !cp_res.valid {
        Logger::sys_warn(
            "user.recovery",
            "Recovery rejected: Opaque token invalid",
            "",
        );
        release_recovery_lock(session_mgr, &token_hash).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Session expired",
        ))));
    }

    let client_device_id =
        crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID)
            .unwrap_or_default();

    let res_val = match release_user_session(
        session_mgr,
        token_mgr,
        config,
        &cp_res.user_id,
        &cp_res.username,
        &cp_res.role,
        cp_res.level,
        &tenant_id,
        &resolved_zone_id,
        &client_device_id,
        &client_device_id,
        "",
    )
    .await
    {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "user.recovery",
                "Failed to release user session in recovery",
                &e.to_string(),
            );
            release_recovery_lock(session_mgr, &token_hash).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue session",
            ))));
        }
    };

    let cache = crate::infra::redis::RecoverySessionCache {
        user_id: cp_res.user_id.clone(),
        role_id: cp_res.role.clone(),
        level: cp_res.level,
        tenant_id: tenant_id.clone(),
        new_jwt: res_val.access_token.clone(),
        new_access_key: res_val.access_key.clone(),
        new_access_secret: res_val.access_secret.clone(),
        zone_id: resolved_zone_id.clone(),
        zone_code: resolved_zone_code.clone(),
    };

    if let Err(e) = session_mgr.set_recovery_cache(&token_hash, &cache).await {
        Logger::sys_error(
            "user.recovery",
            "Failed to save recovery session cache",
            &e.to_string(),
        );
    }

    release_recovery_lock(session_mgr, &token_hash).await;

    Some(build_success_response(
        &cp_res.user_id,
        &cp_res.role,
        cp_res.level,
        &tenant_id,
        &res_val.access_token,
        &res_val.access_key,
        &res_val.access_secret,
        cookie_header,
        &resolved_zone_id,
        &resolved_zone_code,
    ))
}

// ─── SessionManager impl for Recovery Session Helpers ───────────────────────

impl SessionManager {
    /// [COMMENT]: Lấy dữ liệu phục hồi session đã được cache từ Redis L2
    pub async fn get_recovery_cache(
        &self,
        token_hash: &str,
    ) -> Result<Option<RecoverySessionCache>, AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);

        let data: Option<String> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET recovery cache failed: {}", e)))?;

        match data {
            Some(json_str) => {
                let cache = serde_json::from_str(&json_str).map_err(|e| {
                    AcrError::Internal(format!("JSON deserialize recovery cache failed: {}", e))
                })?;
                Ok(Some(cache))
            }
            None => Ok(None),
        }
    }

    /// [COMMENT]: SETNX Lock phục hồi session — TTL 5s để tự giải phóng khi crash
    pub async fn try_lock_recovery(&self, token_hash: &str) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Recovery lock acquisition error: {}", e)))?;

        Ok(acquired)
    }

    /// [COMMENT]: Lưu kết quả phục hồi vào Redis L2 và giải phóng lock (atomic)
    pub async fn set_recovery_cache(
        &self,
        token_hash: &str,
        cache: &RecoverySessionCache,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let json_str = serde_json::to_string(cache).map_err(|e| {
            AcrError::Internal(format!("JSON serialize recovery cache failed: {}", e))
        })?;

        // [COMMENT]: Cache TTL = 5s để phục vụ concurrent requests trong cùng recovery window
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&json_str)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .cmd("DEL")
            .arg(&lock_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Set recovery cache in Redis failed: {}", e))
            })?;

        Ok(())
    }

    /// [COMMENT]: Kiểm tra xem Lock phục hồi còn tồn tại không
    pub async fn is_recovery_locked(&self, token_hash: &str) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let exists: isize = redis::cmd("EXISTS")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("EXISTS lock check failed: {}", e)))?;

        Ok(exists > 0)
    }

    /// [COMMENT]: Giải phóng recovery lock khỏi Redis
    pub async fn release_recovery_lock(&self, token_hash: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("DEL recovery lock failed: {}", e)))?;
        Ok(())
    }
}
