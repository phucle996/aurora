use crate::config::Config;
use crate::error::AcrError;
use crate::infra::redis::{RecoverySessionCache, RedisRuntimeClient, SessionManager};
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::pkg::header::*;
use crate::token::TokenManager;
use crate::user::login::{
    release_user_session, ReleaseUserSessionCommand, UserSessionIssueContext,
};
use crate::user::zone_resolution::resolve_zone_context;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use tonic::{Response, Status};
use uuid::Uuid;

async fn release_recovery_lock(session_mgr: &SessionManager, recovery_key: &str, lock_owner: &str) {
    if let Err(error) = session_mgr
        .release_recovery_lock(recovery_key, lock_owner)
        .await
    {
        Logger::sys_error(
            "user.recovery",
            "Failed to release recovery lock",
            &error.to_string(),
        );
    }
}

struct RecoverySuccessResponse<'a> {
    config: &'a Config,
    user_id: &'a str,
    client_device_id: &'a str,
    level: i32,
    tenant_id: &'a str,
    new_jwt: &'a str,
    new_access_key: &'a str,
    new_access_secret: &'a str,
    zone_id: &'a str,
    zone_code: &'a str,
    context_reset: bool,
}

fn build_success_response(input: RecoverySuccessResponse<'_>) -> Response<CheckResponse> {
    let RecoverySuccessResponse {
        config,
        user_id,
        client_device_id,
        level,
        tenant_id,
        new_jwt,
        new_access_key,
        new_access_secret,
        zone_id,
        zone_code,
        context_reset,
    } = input;
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(")]}}',\n{\"status\":\"ok\"}");

    let domain = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };
    let session_max_age = config.session_ttl_secs;

    builder.add_header(
        "set-cookie",
        format!(
            "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            new_jwt, session_max_age, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        format!(
            "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            new_access_key, session_max_age, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        format!(
            "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            new_access_secret, session_max_age, domain
        ),
        None,
        false,
    );

    builder.add_header(
        "set-cookie",
        format!(
            "client_device_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            client_device_id, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        format!(
            "tenant_id={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            tenant_id, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        format!(
            "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            zone_code, domain
        ),
        None,
        false,
    );

    if context_reset {
        // The expired session requested a tenant that is no longer authorized.
        // Recovery establishes a personal session, but the intercepted request
        // is never forwarded under a different owner context.
        builder.add_header("x-aurora-context-reset", "personal", None, false);
        for cookie_name in [COOKIE_TENANT_DOMAIN, COOKIE_WORKSPACE_ID] {
            builder.add_header(
                "set-cookie",
                format!(
                    "{}=; Path=/; Secure; SameSite=Lax; Max-Age=0{}",
                    cookie_name, domain
                ),
                None,
                false,
            );
        }
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::ok("authorized_via_recovery"));
    response.set_http_response(builder);

    if let Some(ref mut http_response) = response.http_response {
        use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
        if let HttpResponse::DeniedResponse(ref mut denied) = http_response {
            use envoy_types::pb::envoy::config::core::v3::HeaderValueOption;
            for (key, value) in [
                (HEADER_X_USER_ID, user_id.to_string()),
                (HEADER_X_USER_LEVEL, level.to_string()),
                (HEADER_X_TENANT_ID, tenant_id.to_string()),
                (HEADER_X_ZONE_ID, zone_id.to_string()),
            ] {
                let header = HeaderValueOption {
                    header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                        key: key.to_string(),
                        value,
                    }),
                    ..Default::default()
                };
                denied.headers.push(header);
            }
        }
    }

    Response::new(response)
}

fn build_denied_json(
    status: HttpStatusCode,
    message: &str,
    cookie_header: &str,
    clear_credentials: bool,
) -> CheckResponse {
    let error_response = crate::user::login::ErrorResponse {
        error_message: message.to_string(),
        error_code: None,
        verification_email_queued: None,
        mfa_required: None,
        challenge_id: None,
        expires_in: None,
        methods: None,
    };
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(status);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(serde_json::to_string(&error_response).unwrap_or_default());
    if clear_credentials {
        for cookie in clear_all_cookies(cookie_header, "", &["/"]) {
            builder.add_header("set-cookie", &cookie, None, false);
        }
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(builder);
    response
}

/// Recovers an expired user session from a long-lived user/device credential.
/// Tenant context is requested from the current cookie and independently
/// authorized by Controlplane; no claim is decoded from the expired JWT.
pub async fn try_handle_recovery_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    redis_client: &RedisRuntimeClient,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
) -> Option<Result<Response<CheckResponse>, Status>> {
    let refresh_token =
        crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_REFRESH_TOKEN)?;
    if !(64..=512).contains(&refresh_token.len()) {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Session expired",
            cookie_header,
            true,
        ))));
    }

    let requested_tenant_id = match crate::gateway::ext_authz::extract_cookie_value(
        cookie_header,
        COOKIE_TENANT_ID,
    )
    .map(|value| value.trim().to_string())
    {
        None => None,
        Some(value) if value.is_empty() || value == "platform" => None,
        Some(value) if matches!(Uuid::parse_str(&value), Ok(tenant_id) if !tenant_id.is_nil()) => {
            Some(value)
        }
        Some(_) => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Invalid session context",
                cookie_header,
                false,
            ))));
        }
    };

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) =
        match resolve_zone_context(shared_redis, redis_client, cookie_header, client_headers).await
        {
            Ok(resolved) => resolved,
            Err(_) => {
                return Some(Ok(Response::new(build_denied_json(
                    HttpStatusCode::BadRequest,
                    "A concrete zone is required",
                    cookie_header,
                    false,
                ))));
            }
        };
    if resolved_zone_code == "global"
        || resolved_zone_id == "00000000-0000-0000-0000-000000000000"
        || (resolved_zone_status != "active" && resolved_zone_status != "draining")
    {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Zone unavailable",
            cookie_header,
            false,
        ))));
    }

    let token_hash = crate::gateway::ext_authz::sha256_hash(&refresh_token);
    let requested_scope = requested_tenant_id.as_deref().unwrap_or("platform");
    // Zone and requested context are part of the key because cached access
    // credentials carry both. A token-only key could replay a tenant session
    // into a concurrent personal or different-Zone request.
    let recovery_key = crate::gateway::ext_authz::sha256_hash(&format!(
        "{}:{}:{}",
        token_hash, resolved_zone_id, requested_scope
    ));

    if let Ok(Some(cache)) = session_mgr.get_recovery_cache(&recovery_key).await {
        return Some(Ok(build_success_response(RecoverySuccessResponse {
            config,
            user_id: &cache.user_id,
            client_device_id: &cache.client_device_id,
            level: cache.level,
            tenant_id: &cache.tenant_id,
            new_jwt: &cache.new_jwt,
            new_access_key: &cache.new_access_key,
            new_access_secret: &cache.new_access_secret,
            zone_id: &cache.zone_id,
            zone_code: &cache.zone_code,
            context_reset: cache.context_reset,
        })));
    }

    if let Ok(true) = session_mgr.is_recovery_locked(&recovery_key).await {
        for _ in 0..12 {
            tokio::time::sleep(Duration::from_millis(100)).await;
            if let Ok(Some(cache)) = session_mgr.get_recovery_cache(&recovery_key).await {
                return Some(Ok(build_success_response(RecoverySuccessResponse {
                    config,
                    user_id: &cache.user_id,
                    client_device_id: &cache.client_device_id,
                    level: cache.level,
                    tenant_id: &cache.tenant_id,
                    new_jwt: &cache.new_jwt,
                    new_access_key: &cache.new_access_key,
                    new_access_secret: &cache.new_access_secret,
                    zone_id: &cache.zone_id,
                    zone_code: &cache.zone_code,
                    context_reset: cache.context_reset,
                })));
            }
        }
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::ServiceUnavailable,
            "Session recovery is busy",
            cookie_header,
            false,
        ))));
    }

    let lock_owner = Uuid::now_v7().to_string();
    let acquired = session_mgr
        .try_lock_recovery(&recovery_key, &lock_owner)
        .await
        .unwrap_or(false);
    if !acquired {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::ServiceUnavailable,
            "Session recovery is busy",
            cookie_header,
            false,
        ))));
    }

    let request = crate::infra::iam_proto::auth::RecoverUserSessionRequest {
        refresh_token: refresh_token.clone(),
        requested_tenant_id: requested_tenant_id.clone(),
    };
    let mut request_bytes = Vec::new();
    if request.encode(&mut request_bytes).is_err() {
        release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::InternalServerError,
            "Session recovery unavailable",
            cookie_header,
            false,
        ))));
    }

    let response_bytes = match shared_redis
        .request(
            "iam.auth.recover_user_session",
            "iam.auth.recover_user_session.reply.",
            request_bytes,
            Duration::from_millis(800),
        )
        .await
    {
        Ok(payload) => payload,
        Err(error) => {
            Logger::sys_error(
                "user.recovery",
                "Controlplane recovery request failed",
                &error,
            );
            release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::ServiceUnavailable,
                "Session recovery unavailable",
                cookie_header,
                false,
            ))));
        }
    };
    let response = match crate::infra::iam_proto::auth::RecoverUserSessionResponse::decode(
        response_bytes.as_slice(),
    ) {
        Ok(decoded) => decoded,
        Err(error) => {
            Logger::sys_error(
                "user.recovery",
                "Controlplane recovery response decode failed",
                &error.to_string(),
            );
            release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::ServiceUnavailable,
                "Session recovery unavailable",
                cookie_header,
                false,
            ))));
        }
    };

    if !response.credential_valid {
        release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            "Session expired",
            cookie_header,
            true,
        ))));
    }

    let context_reset = !response.context_authorized
        && requested_tenant_id.is_some()
        && response.personal_fallback_authorized;
    if !response.context_authorized && !context_reset {
        release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Session context is unavailable",
            cookie_header,
            false,
        ))));
    }

    let user_id_valid =
        matches!(Uuid::parse_str(&response.user_id), Ok(user_id) if !user_id.is_nil());
    let client_device_id_valid = matches!(
        Uuid::parse_str(&response.client_device_id),
        Ok(device_id) if !device_id.is_nil()
    );
    let resolved_tenant_id = response.resolved_tenant_id.trim();
    let context_binding_valid = if context_reset {
        resolved_tenant_id.is_empty()
    } else if let Some(requested_tenant_id) = requested_tenant_id.as_deref() {
        response.context_authorized && resolved_tenant_id == requested_tenant_id
    } else {
        response.context_authorized && resolved_tenant_id.is_empty()
    };
    let tenant_id = if resolved_tenant_id.is_empty() {
        "platform".to_string()
    } else if matches!(
        Uuid::parse_str(resolved_tenant_id),
        Ok(tenant_id) if !tenant_id.is_nil()
    ) {
        resolved_tenant_id.to_string()
    } else {
        String::new()
    };
    if !user_id_valid
        || !client_device_id_valid
        || !context_binding_valid
        || response.username.trim().is_empty()
        || response.role_level < 0
        || tenant_id.is_empty()
    {
        release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::ServiceUnavailable,
            "Session recovery unavailable",
            cookie_header,
            false,
        ))));
    }

    let release_tenant_id = if tenant_id == "platform" {
        ""
    } else {
        tenant_id.as_str()
    };
    let released = match release_user_session(
        UserSessionIssueContext {
            session_mgr: session_mgr.as_ref(),
            token_mgr: token_mgr.as_ref(),
            shared_redis: shared_redis.as_ref(),
            config,
        },
        ReleaseUserSessionCommand {
            user_id: &response.user_id,
            username: &response.username,
            level: response.role_level,
            tenant_id: release_tenant_id,
            zone_id: &resolved_zone_id,
            device_id: &response.client_device_id,
            client_device_id: &response.client_device_id,
            client_proof_public_key: "",
        },
    )
    .await
    {
        Ok(session) => session,
        Err(error) => {
            Logger::sys_error(
                "user.recovery",
                "Failed to issue recovered session",
                &error.to_string(),
            );
            release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::ServiceUnavailable,
                "Session recovery unavailable",
                cookie_header,
                false,
            ))));
        }
    };

    let cache = RecoverySessionCache {
        user_id: response.user_id.clone(),
        client_device_id: response.client_device_id.clone(),
        level: response.role_level,
        tenant_id: tenant_id.clone(),
        new_jwt: released.access_token.clone(),
        new_access_key: released.access_key.clone(),
        new_access_secret: released.access_secret.clone(),
        zone_id: resolved_zone_id.clone(),
        zone_code: resolved_zone_code.clone(),
        context_reset,
    };
    if let Err(error) = session_mgr
        .set_recovery_cache(&recovery_key, &lock_owner, &cache)
        .await
    {
        Logger::sys_error(
            "user.recovery",
            "Failed to publish recovered session cache",
            &error.to_string(),
        );
        release_recovery_lock(session_mgr, &recovery_key, &lock_owner).await;
    }

    Some(Ok(build_success_response(RecoverySuccessResponse {
        config,
        user_id: &response.user_id,
        client_device_id: &response.client_device_id,
        level: response.role_level,
        tenant_id: &tenant_id,
        new_jwt: &released.access_token,
        new_access_key: &released.access_key,
        new_access_secret: &released.access_secret,
        zone_id: &resolved_zone_id,
        zone_code: &resolved_zone_code,
        context_reset,
    })))
}

impl SessionManager {
    pub async fn get_recovery_cache(
        &self,
        recovery_key: &str,
    ) -> Result<Option<RecoverySessionCache>, AcrError> {
        let mut connection = self.get_connection().await?;
        let redis_key = format!("iam:recovery:{{{recovery_key}}}:cache");
        let data: Option<String> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut connection)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("GET recovery cache failed: {}", error))
            })?;
        match data {
            Some(value) => serde_json::from_str(&value).map(Some).map_err(|error| {
                AcrError::Internal(format!("Decode recovery cache failed: {}", error))
            }),
            None => Ok(None),
        }
    }

    pub async fn try_lock_recovery(
        &self,
        recovery_key: &str,
        lock_owner: &str,
    ) -> Result<bool, AcrError> {
        let mut connection = self.get_connection().await?;
        let lock_key = format!("iam:recovery:{{{recovery_key}}}:lock");
        redis::cmd("SET")
            .arg(&lock_key)
            .arg(lock_owner)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut connection)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Recovery lock acquisition failed: {}", error))
            })
    }

    pub async fn set_recovery_cache(
        &self,
        recovery_key: &str,
        lock_owner: &str,
        cache: &RecoverySessionCache,
    ) -> Result<(), AcrError> {
        let mut connection = self.get_connection().await?;
        // The lock and cache participate in one Lua transaction, so they must
        // be pinned to the same Redis Cluster hash slot.
        let cache_key = format!("iam:recovery:{{{recovery_key}}}:cache");
        let lock_key = format!("iam:recovery:{{{recovery_key}}}:lock");
        let encoded = serde_json::to_string(cache).map_err(|error| {
            AcrError::Internal(format!("Encode recovery cache failed: {}", error))
        })?;
        let stored: i32 = redis::Script::new(
            r#"
            if redis.call('GET', KEYS[1]) ~= ARGV[1] then
                return 0
            end
            redis.call('SET', KEYS[2], ARGV[2], 'EX', ARGV[3])
            redis.call('DEL', KEYS[1])
            return 1
            "#,
        )
        .key(&lock_key)
        .key(&cache_key)
        .arg(lock_owner)
        .arg(encoded)
        .arg(5)
        .invoke_async(&mut connection)
        .await
        .map_err(|error| AcrError::RedisError(format!("Set recovery cache failed: {}", error)))?;
        if stored != 1 {
            return Err(AcrError::RedisError(
                "Recovery lock ownership was lost before cache publish".to_string(),
            ));
        }
        Ok(())
    }

    pub async fn is_recovery_locked(&self, recovery_key: &str) -> Result<bool, AcrError> {
        let mut connection = self.get_connection().await?;
        let lock_key = format!("iam:recovery:{{{recovery_key}}}:lock");
        let exists: isize = redis::cmd("EXISTS")
            .arg(&lock_key)
            .query_async(&mut connection)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Recovery lock lookup failed: {}", error))
            })?;
        Ok(exists > 0)
    }

    pub async fn release_recovery_lock(
        &self,
        recovery_key: &str,
        lock_owner: &str,
    ) -> Result<(), AcrError> {
        let mut connection = self.get_connection().await?;
        let lock_key = format!("iam:recovery:{{{recovery_key}}}:lock");
        let _: i32 = redis::Script::new(
            r#"
            if redis.call('GET', KEYS[1]) == ARGV[1] then
                return redis.call('DEL', KEYS[1])
            end
            return 0
            "#,
        )
        .key(&lock_key)
        .arg(lock_owner)
        .invoke_async(&mut connection)
        .await
        .map_err(|error| {
            AcrError::RedisError(format!("Release recovery lock failed: {}", error))
        })?;
        Ok(())
    }
}

#[cfg(test)]
#[path = "../../tests/unit/user/recovery.rs"]
mod tests;
