// ======================================================================================================
// 📂 sre/rotate.rs — handle_sre_session_rotation: Sliding Session cho SRE
//
// 📌 VAI TRÒ:
//   - Tự động gia hạn Trinity Session khi TTL còn thấp (dưới refresh_threshold_secs).
//   - Dùng SETNX Distributed Lock để chống Race Condition khi nhiều request song song cùng rotate.
//   - Trả về Vec<String> cookies mới để set qua Envoy response headers.
// ======================================================================================================

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::sre::claims::{SreClaims, SreTokenManager};
use std::sync::Arc;

/// [COMMENT]: Xử lý Sliding Session (Trinity Refresh) cho SRE nếu TTL còn thấp.
/// Trả về danh sách Set-Cookie string để inject vào response headers.
pub async fn handle_sre_session_rotation(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<SreTokenManager>,
    config: &Config,
    claims: &SreClaims,
    access_key: &str,
) -> Vec<String> {
    let now = chrono::Utc::now().timestamp();
    // [COMMENT]: Tính remaining_ttl dựa trên claims.exp thực tế (không dùng lsa để tránh sliding không kích hoạt)
    let remaining_ttl = if claims.exp > now {
        (claims.exp - now) as u64
    } else {
        0
    };

    let mut cookies_to_set = Vec::new();
    if remaining_ttl <= config.refresh_threshold_secs {
        Logger::sys_info(
            "sre.rotate",
            &format!(
                "TTL low ({}s) for SRE. Initiating transparent refresh.",
                remaining_ttl
            ),
        );

        // [COMMENT]: Sinh bộ Trinity mới
        let new_access_key = uuid::Uuid::now_v7().to_string();
        let new_access_secret = uuid::Uuid::new_v4().to_string();
        let new_ash = sha256_hash(&new_access_secret);

        let new_claims = SreClaims {
            sub: claims.sub.clone(),
            zone_id: claims.zone_id.clone(),
            access_key: new_access_key.clone(),
            iss: claims.iss.clone(),
            exp: chrono::Utc::now().timestamp() + config.session_ttl_secs as i64,
            iat: chrono::Utc::now().timestamp(),
        };

        if let Ok(new_jwt) = token_mgr.generate_token(&new_claims).await {
            // [COMMENT]: Ghi nhận session mới lên Redis — SETNX Lock chống race condition
            match session_mgr
                .try_rotate_sre_session(access_key, &new_access_key, &new_ash)
                .await
            {
                Ok(true) => {
                    // [COMMENT]: Rotation thành công — set cookies mới Path=/admin
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
                    Logger::sys_info("sre.rotate", "SRE session rotated successfully");
                }
                Ok(false) => {
                    // [COMMENT]: Lock bị chiếm — request khác đang rotate — bỏ qua
                    Logger::sys_debug(
                        "sre.rotate",
                        "SRE session rotation already in progress, bypassing",
                    );
                }
                Err(e) => {
                    Logger::sys_error("sre.rotate", "Failed to rotate SRE session", &e.to_string());
                }
            }
        }
    }

    cookies_to_set
}

// ─── Helper ────────────────────────────────────────────────────────────────────

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
