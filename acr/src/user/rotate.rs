// ======================================================================================================
// 📂 user/rotate.rs — Handle user session rotation (sliding session)
// ======================================================================================================

use crate::config::Config;
use crate::error::AcrError;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::token::TokenManager;
use crate::user::claims::Claims;
use crate::user::session::UserAccessSession;
use prost::Message;
use std::sync::Arc;

pub struct RotateSessionCommand<'a> {
    pub zone_id: &'a str,
    pub tenant_id: &'a str,
    pub user_id: &'a str,
    pub old_access_key: &'a str,
    pub new_access_key: &'a str,
    pub new_access_secret_hash: &'a str,
    pub device_id: &'a str,
}

/// [COMMENT]: Xử lý Sliding Session (Trinity Refresh) cho User thường khi TTL còn thấp
pub async fn handle_user_session_rotation(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    claims: &Claims,
    access_key: &str,
    cookie_header: &str,
) -> Vec<String> {
    let now = chrono::Utc::now().timestamp();
    let remaining_ttl = if claims.exp > now {
        (claims.exp - now) as u64
    } else {
        0
    };

    let mut cookies_to_set = Vec::new();
    if remaining_ttl <= config.refresh_threshold_secs {
        Logger::sys_info(
            "user.rotate",
            &format!(
                "TTL low ({}s) for user sub={}. Initiating transparent refresh.",
                remaining_ttl, claims.sub
            ),
        );

        let device_id =
            crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID)
                .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

        let new_access_key = uuid::Uuid::now_v7().to_string();
        let new_access_secret = uuid::Uuid::new_v4().to_string();
        let new_ash = sha256_hash(&new_access_secret);

        let new_claims = Claims {
            sub: claims.sub.clone(),
            uid: claims.uid.clone(),
            lvl: claims.lvl,
            tenant_id: claims.tenant_id.clone(),
            zone_id: claims.zone_id.clone(),
            access_key: new_access_key.clone(),
            iss: claims.iss.clone(),
            exp: chrono::Utc::now().timestamp() + config.session_ttl_secs as i64,
            iat: chrono::Utc::now().timestamp(),
        };

        if let Ok(new_jwt) = token_mgr.generate_token(&new_claims).await {
            match session_mgr
                .try_rotate_session(RotateSessionCommand {
                    zone_id: claims.zone_id.as_deref().unwrap_or("global"),
                    tenant_id: claims.tenant_id.as_deref().unwrap_or("platform"),
                    user_id: &claims.uid,
                    old_access_key: access_key,
                    new_access_key: &new_access_key,
                    new_access_secret_hash: &new_ash,
                    device_id: &device_id,
                })
                .await
            {
                Ok(true) => {
                    cookies_to_set.push(format!(
                        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                        new_jwt
                    ));
                    cookies_to_set.push(format!(
                        "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                        new_access_key
                    ));
                    cookies_to_set.push(format!(
                        "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                        new_access_secret
                    ));
                    Logger::sys_info("user.rotate", "User session rotated successfully");
                }
                Ok(false) => {
                    Logger::sys_debug(
                        "user.rotate",
                        "User session rotation already in progress, bypassing",
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "user.rotate",
                        "Failed to rotate user session",
                        &e.to_string(),
                    );
                }
            }
        }
    }

    cookies_to_set
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

// ─── SessionManager impl for User Session Rotation ──────────────────────────

impl SessionManager {
    /// [COMMENT]: Trinity Rotation User — SETNX Lock chống Race Condition, Grace Period 5s
    pub async fn try_rotate_session(
        &self,
        command: RotateSessionCommand<'_>,
    ) -> Result<bool, AcrError> {
        let RotateSessionCommand {
            zone_id,
            tenant_id,
            user_id,
            old_access_key,
            new_access_key,
            new_access_secret_hash,
            device_id,
        } = command;
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:refresh:{}", old_access_key);

        // [COMMENT]: 1. SETNX Lock — TTL 5s auto-release khi crash
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Lock acquisition error: {}", e)))?;

        if !acquired {
            // [COMMENT]: Lock bị chiếm — luồng khác đang rotate — tiếp tục với session cũ
            return Ok(false);
        }

        let old_redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, old_access_key
        );
        // [COMMENT]: Rotation phải mang nguyên session-proof key sang access_key mới; không nhận key từ client.
        let old_bytes: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&old_redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Load old session failed: {}", e)))?;
        let old_session = old_bytes
            .ok_or_else(|| {
                AcrError::Internal("Old session disappeared during rotation".to_string())
            })
            .and_then(|bytes| {
                UserAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AcrError::Internal(e.to_string()))
            })?;
        let new_redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, new_access_key
        );
        let index_key = format!("iam:user_access_index:{}", user_id);
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        let now = chrono::Utc::now().timestamp();
        let new_session = UserAccessSession {
            ash: new_access_secret_hash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
            client_proof_public_key: old_session.client_proof_public_key,
        };

        let mut buf = Vec::new();
        new_session
            .encode(&mut buf)
            .map_err(|e| AcrError::Internal(e.to_string()))?;

        // Session is authoritative; indexes are idempotent discovery data.
        // Write the new credential first so a failed cross-slot cleanup never
        // invalidates the caller before a retry can complete the rotation.
        redis::cmd("SET")
            .arg(&new_redis_key)
            .arg(&buf)
            .arg("EX")
            .arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Create rotated session failed: {}", e)))?;
        redis::cmd("EXPIRE")
            .arg(&old_redis_key)
            .arg(5)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Shorten old session failed: {}", e)))?;
        redis::cmd("SADD")
            .arg(&index_key)
            .arg(&new_redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Index rotated session failed: {}", e)))?;
        redis::cmd("SREM")
            .arg(&index_key)
            .arg(&old_redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Remove old session index failed: {}", e)))?;
        redis::cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Set user index TTL failed: {}", e)))?;
        redis::cmd("SADD")
            .arg(&dev_index_key)
            .arg(&new_redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Index rotated device failed: {}", e)))?;
        redis::cmd("SREM")
            .arg(&dev_index_key)
            .arg(&old_redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Remove old device index failed: {}", e)))?;
        redis::cmd("EXPIRE")
            .arg(&dev_index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Set device index TTL failed: {}", e)))?;

        // [COMMENT]: 3. Giải phóng lock sớm (không cần chờ 5s hết hạn)
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }
}
