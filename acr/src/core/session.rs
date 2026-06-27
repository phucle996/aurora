use crate::config::Config;
use crate::error::AcrError;
use prost::Message;
use std::sync::Arc;

// Định nghĩa Protobuf struct tương thích 100% với iamproto.UserAccessSession của Go
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct UserAccessSession {
    // Access Secret Hash (Băm SHA-256 của Access Secret thô)
    #[prost(string, tag = "1")]
    pub ash: ::prost::alloc::string::String,
    // Tracked Device ID (UUID của thiết bị đã đăng nhập)
    #[prost(string, tag = "2")]
    pub tdid: ::prost::alloc::string::String,
    // Last Seen At (Unix timestamp ghi nhận hoạt động cuối)
    #[prost(int64, tag = "3")]
    pub lsa: i64,
}

// [COMMENT]: Định nghĩa Protobuf struct tương thích 100% với AdminAccessSession được khai báo ở Go/Proto
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct AdminAccessSession {
    // [COMMENT]: Access Secret Hash (Băm SHA-256 của Access Secret thô)
    #[prost(string, tag = "1")]
    pub access_secret_hash: ::prost::alloc::string::String,

    // [COMMENT]: Khóa công khai Ed25519 của thiết bị dùng để xác thực chữ ký của các thao tác critical
    #[prost(string, tag = "2")]
    pub device_public_key: ::prost::alloc::string::String,
}

pub struct SessionManager {
    redis_client: Arc<redis::Client>,
    config: Config,
}

impl SessionManager {
    pub fn new(redis_client: Arc<redis::Client>, config: Config) -> Self {
        Self {
            redis_client,
            config,
        }
    }

    // Hỗ trợ kết nối bất đồng bộ sang Redis
    pub(crate) async fn get_connection(&self) -> Result<redis::aio::Connection, AcrError> {
        self.redis_client
            .get_tokio_connection()
            .await
            .map_err(|e| AcrError::RedisError(format!("Failed to get Redis connection: {}", e)))
    }
}

// [COMMENT]: Struct chứa thông tin session phục hồi thành công để chia sẻ giữa các luồng song song
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct RecoverySessionCache {
    pub user_id: String,
    pub role: String,
    pub level: i32,
    pub tenant_id: String,
    pub new_jwt: String,
    pub new_access_key: String,
    pub new_access_secret: String,
    pub zone_id: String,
    pub zone_code: String,
}

// [COMMENT]: Tách biệt việc triển khai logic session cho User và SRE Admin bằng cách nạp trực tiếp qua macro compile-time include!
include!("session/user_session.rs");
include!("session/admin_session.rs");
