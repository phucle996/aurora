// ======================================================================================================
// 📂 user/rotate.rs — Handle user session rotation (sliding session)
// ======================================================================================================

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::user::claims::Claims;
use std::sync::Arc;

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
            &format!("TTL low ({}s) for user sub={}. Initiating transparent refresh.", remaining_ttl, claims.sub),
        );

        let device_id = crate::gateway::ext_authz::extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID)
            .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

        let new_access_key = uuid::Uuid::now_v7().to_string();
        let new_access_secret = uuid::Uuid::new_v4().to_string();
        let new_ash = sha256_hash(&new_access_secret);

        let new_claims = Claims {
            sub: claims.sub.clone(),
            uid: claims.uid.clone(),
            role_id: claims.role_id.clone(),
            lvl: claims.lvl,
            tenant_id: claims.tenant_id.clone(),
            zone_id: claims.zone_id.clone(),
            access_key: new_access_key.clone(),
            jti: uuid::Uuid::new_v4().to_string(),
            iss: claims.iss.clone(),
            exp: chrono::Utc::now().timestamp() + config.session_ttl_secs as i64,
            iat: chrono::Utc::now().timestamp(),
        };

        if let Ok(new_jwt) = token_mgr.generate_token(&new_claims).await {
            match session_mgr
                .try_rotate_session(
                    claims.zone_id.as_deref().unwrap_or("global"),
                    claims.tenant_id.as_deref().unwrap_or("platform"),
                    &claims.uid,
                    access_key,
                    &new_access_key,
                    &new_ash,
                    &device_id,
                )
                .await
            {
                Ok(true) => {
                    cookies_to_set.push(format!("access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax", new_jwt));
                    cookies_to_set.push(format!("access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax", new_access_key));
                    cookies_to_set.push(format!("access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax", new_access_secret));
                    Logger::sys_info("user.rotate", "User session rotated successfully");
                }
                Ok(false) => {
                    Logger::sys_debug("user.rotate", "User session rotation already in progress, bypassing");
                }
                Err(e) => {
                    Logger::sys_error("user.rotate", "Failed to rotate user session", &e.to_string());
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
