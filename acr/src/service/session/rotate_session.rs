use crate::config::Config;
use crate::core::session::{SessionManager, UserAccessSession};
use crate::core::token::{Claims, TokenManager};
use crate::observability::logger::Logger;
use crate::service::ext_authz::sha256_hash;
use std::sync::Arc;

// [COMMENT]: Xử lý Sliding Session (Trinity Refresh / Session Rotation) nếu TTL của session còn thấp
pub async fn handle_session_rotation(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    claims: &Claims,
    session: &UserAccessSession,
    access_key: &str,
) -> Vec<String> {
    let now = chrono::Utc::now().timestamp();
    // [COMMENT]: Tính remaining_ttl dựa trên thời hạn hết hạn thực tế của JWT (claims.exp)
    // thay vì dựa trên session.lsa — vì lsa bị throttle update liên tục (30s)
    // dẫn đến session_age luôn nhỏ → remaining_ttl luôn cao → sliding session không bao giờ kích hoạt.
    let remaining_ttl = if claims.exp > now {
        (claims.exp - now) as u64
    } else {
        0
    };

    let mut cookies_to_set = Vec::new();
    if remaining_ttl <= config.refresh_threshold_secs {
        Logger::sys_info(
            "ext_authz.refresh",
            &format!(
                "TTL low ({}s) for user={}. Initiating transparent refresh.",
                remaining_ttl, claims.sub
            ),
        );

        // [COMMENT]: Tạo mới bộ Trinity Credentials (access_key, access_secret)
        let new_access_key = uuid::Uuid::now_v7().to_string();
        let new_access_secret = uuid::Uuid::new_v4().to_string();
        let new_ash = sha256_hash(&new_access_secret);

        let new_claims = Claims {
            sub: claims.sub.clone(),
            uid: claims.uid.clone(),
            // [COMMENT]: Giữ nguyên role_id UUID khi rotation — không thông dịch sang role code
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
            // [COMMENT]: Thực hiện ghi nhận session mới lên Redis với cơ chế khóa SETNX chống race condition
            match session_mgr
                .try_rotate_session(
                    claims.zone_id.as_deref().unwrap_or("global"),
                    // [COMMENT]: Sử dụng "platform" thay vì "global" làm fallback cho tenant_id
                    claims.tenant_id.as_deref().unwrap_or("platform"),
                    &claims.uid,
                    access_key,
                    &new_access_key,
                    &new_ash,
                    &session.tdid,
                )
                .await
            {
                Ok(true) => {
                    // [COMMENT]: Rotation thành công -> chuẩn bị Cookie mới trả về cho client qua Envoy
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
                    Logger::sys_info(
                        "ext_authz.refresh",
                        &format!("Session rotated successfully for user={}", claims.sub),
                    );
                }
                Ok(false) => {
                    // [COMMENT]: Lock bị chiếm bởi request khác đang song song refresh -> cho request này dùng session cũ đi tiếp
                    Logger::sys_debug(
                        "ext_authz.refresh",
                        &format!(
                            "Session rotation already in progress for user={}, bypassing",
                            claims.sub
                        ),
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "ext_authz.refresh",
                        "Failed to rotate session",
                        &e.to_string(),
                    );
                }
            }
        }
    }

    cookies_to_set
}

// [COMMENT]: Xử lý Sliding Session (Trinity Refresh / Session Rotation) cho SRE Admin nếu TTL của session còn thấp
pub async fn handle_admin_session_rotation(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    claims: &Claims,
    access_key: &str,
) -> Vec<String> {
    let now = chrono::Utc::now().timestamp();
    // [COMMENT]: Tính remaining_ttl dựa trên thời hạn hết hạn thực tế của JWT (claims.exp)
    let remaining_ttl = if claims.exp > now {
        (claims.exp - now) as u64
    } else {
        0
    };

    let mut cookies_to_set = Vec::new();
    if remaining_ttl <= config.refresh_threshold_secs {
        Logger::sys_info(
            "ext_authz.admin_refresh",
            &format!(
                "TTL low ({}s) for admin. Initiating transparent refresh.",
                remaining_ttl
            ),
        );

        // [COMMENT]: Tạo mới bộ Trinity Credentials (access_key, access_secret)
        let new_access_key = uuid::Uuid::now_v7().to_string();
        let new_access_secret = uuid::Uuid::new_v4().to_string();
        let new_ash = sha256_hash(&new_access_secret);

        let new_claims = Claims {
            sub: claims.sub.clone(),
            uid: claims.uid.clone(),
            // [COMMENT]: Giữ nguyên role_id UUID khi admin session rotation
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
            // [COMMENT]: Thực hiện ghi nhận session mới lên Redis với cơ chế khóa SETNX chống race condition
            match session_mgr
                .try_rotate_admin_session(
                    access_key,
                    &new_access_key,
                    &new_ash,
                )
                .await
            {
                Ok(true) => {
                    // [COMMENT]: Rotation thành công -> chuẩn bị Cookie mới trả về cho SRE Admin qua Envoy
                    cookies_to_set.push(format!(
                        "access_token={}; Path=/admin; HttpOnly; Secure; SameSite=Lax",
                        new_jwt
                    ));
                    cookies_to_set.push(format!(
                        "access_key={}; Path=/admin; HttpOnly; Secure; SameSite=Lax",
                        new_access_key
                    ));
                    cookies_to_set.push(format!(
                        "access_secret={}; Path=/admin; HttpOnly; Secure; SameSite=Lax",
                        new_access_secret
                    ));
                    Logger::sys_info(
                        "ext_authz.admin_refresh",
                        "Admin session rotated successfully",
                    );
                }
                Ok(false) => {
                    // [COMMENT]: Lock bị chiếm bởi request khác đang song song refresh -> cho request này dùng session cũ đi tiếp
                    Logger::sys_debug(
                        "ext_authz.admin_refresh",
                        "Admin session rotation already in progress, bypassing",
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "ext_authz.admin_refresh",
                        "Failed to rotate admin session",
                        &e.to_string(),
                    );
                }
            }
        }
    }

    cookies_to_set
}

