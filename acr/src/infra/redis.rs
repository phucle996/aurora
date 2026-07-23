// ======================================================================================================
// 📂 infra/redis.rs — SessionManager dùng chung cho toàn bộ domain (User, SRE, Billing)
//
// 📌 VAI TRÒ:
//   - Quản lý kết nối Redis L2 (HA) dùng chung cho mọi domain session.
//   - Cung cấp get_connection() làm entry-point cho mọi Security-State Redis op.
//   - RecoverySessionCache: struct dùng khi session recovery thành công.
//   - Các impl cụ thể (user, admin, billing session ops) được viết trong từng domain file.
// ======================================================================================================

use crate::config::Config;
use crate::error::AcrError;
use std::sync::Arc;

/// [COMMENT]: SessionManager — quản lý kết nối Redis L2 tập trung.
/// Được chia sẻ qua Arc<SessionManager> giữa tất cả domain (user, sre, billing).
pub struct SessionManager {
    pub(crate) redis_client: Arc<redis::Client>,
    pub(crate) config: Config,
}

impl SessionManager {
    /// [COMMENT]: Khởi tạo SessionManager với Redis client và config.
    pub fn new(redis_client: Arc<redis::Client>, config: Config) -> Self {
        Self {
            redis_client,
            config,
        }
    }

    /// [COMMENT]: Lấy kết nối async từ Redis pool (tokio connection).
    pub(crate) async fn get_connection(&self) -> Result<redis::aio::Connection, AcrError> {
        self.redis_client
            .get_tokio_connection()
            .await
            .map_err(|e| AcrError::RedisError(format!("Failed to get Redis connection: {}", e)))
    }
}

/// [COMMENT]: RecoverySessionCache — thông tin session phục hồi thành công.
/// Dùng để trao đổi dữ liệu giữa recovery_session và các handler khác.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct RecoverySessionCache {
    pub user_id: String,
    pub role_id: String,
    pub level: i32,
    pub tenant_id: String,
    pub new_jwt: String,
    pub new_access_key: String,
    pub new_access_secret: String,
    pub zone_id: String,
    pub zone_code: String,
}
