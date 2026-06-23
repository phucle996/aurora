use crate::config::Config;
use crate::error::AclError;
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

    // Lấy thông tin Session từ Redis L2
    pub async fn get_session(
        &self,
        user_id: &str,
        access_key: &str,
    ) -> Result<Option<UserAccessSession>, AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:user_access_session:{}:{}", user_id, access_key);

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("GET session failed: {}", e)))?;

        match data {
            Some(bytes) => {
                let session = UserAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AclError::Internal(format!("Protobuf decode failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    // Đăng ký Session mới (Gọi khi Login thành công từ IAM)
    pub async fn register_session(
        &self,
        user_id: &str,
        access_key: &str,
        ash: &str,
        device_id: &str,
    ) -> Result<(), AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:user_access_session:{}:{}", user_id, access_key);

        let now = chrono::Utc::now().timestamp();
        let session = UserAccessSession {
            ash: ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        // Serialize sang dạng Protobuf binary trước khi ghi xuống Redis
        let mut buf = Vec::new();
        session
            .encode(&mut buf)
            .map_err(|e| AclError::Internal(format!("Protobuf encode failed: {}", e)))?;

        // Ghi session kèm thời gian sống (TTL) và đồng bộ index set của user
        let index_key = format!("iam:user_access_index:{}", user_id);
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(self.config.session_ttl_secs)
            .cmd("SADD")
            .arg(&index_key)
            .arg(access_key)
            .cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AclError::RedisError(format!("Register session in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // Throttled update cho Last Seen At (Giảm tải số lệnh ghi vào Redis L2)
    pub async fn update_last_seen(
        &self,
        user_id: &str,
        access_key: &str,
        last_lsa: i64,
    ) -> Result<(), AclError> {
        let now = chrono::Utc::now().timestamp();

        // Chỉ ghi đè Redis nếu thời gian cũ lệch quá 30 giây so với hiện tại
        if now - last_lsa < 30 {
            return Ok(());
        }

        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:user_access_session:{}:{}", user_id, access_key);

        // Lấy session hiện tại
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(e.to_string()))?;

        if let Some(bytes) = data {
            let mut session = UserAccessSession::decode(bytes.as_slice())
                .map_err(|e| AclError::Internal(e.to_string()))?;

            // Cập nhật timestamp mới
            session.lsa = now;
            let mut buf = Vec::new();
            session
                .encode(&mut buf)
                .map_err(|e| AclError::Internal(e.to_string()))?;

            // Ghi đè vào Redis, giữ nguyên TTL
            let ttl: u64 = redis::cmd("TTL")
                .arg(&redis_key)
                .query_async(&mut conn)
                .await
                .unwrap_or(self.config.session_ttl_secs);

            redis::pipe()
                .atomic()
                .cmd("SET")
                .arg(&redis_key)
                .arg(&buf)
                .cmd("EXPIRE")
                .arg(&redis_key)
                .arg(ttl)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|e| AclError::RedisError(format!("Update LSA failed: {}", e)))?;
        }

        Ok(())
    }

    // Xoay vòng Session (Trinity Refresh) có bảo vệ chống Race Condition bằng Distributed Lock
    pub async fn try_rotate_session(
        &self,
        user_id: &str,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
        device_id: &str,
    ) -> Result<bool, AclError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:refresh:{}", old_access_key);

        // 1. Cố gắng giành quyền Lock bằng SETNX (TTL 5 giây để tự giải phóng nếu crash)
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("Lock acquisition error: {}", e)))?;

        if !acquired {
            // Không lấy được lock -> luồng song song khác đang refresh -> cho phép đi tiếp bằng session cũ
            return Ok(false);
        }

        // 2. Tiến hành xoay vòng session
        let old_redis_key = format!("iam:user_access_session:{}:{}", user_id, old_access_key);
        let new_redis_key = format!("iam:user_access_session:{}:{}", user_id, new_access_key);
        let index_key = format!("iam:user_access_index:{}", user_id);

        let now = chrono::Utc::now().timestamp();
        let new_session = UserAccessSession {
            ash: new_ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        let mut buf = Vec::new();
        new_session
            .encode(&mut buf)
            .map_err(|e| AclError::Internal(e.to_string()))?;

        // 3. Thực thi Pipeline nguyên tử:
        // - Tạo session mới với đầy đủ TTL
        // - [COMMENT]: Chuyển session cũ sang thời gian sống ngắn hạn (Grace Period - hardcode 5 giây) để tránh lỗi 401 cho các request đang bay lơ lửng.
        // - [COMMENT]: Thêm AccessKey mới vào index và xoá AccessKey cũ để đảm bảo tính năng đăng xuất thiết bị khác/thu hồi hoạt động chính xác.
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&new_redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&new_redis_key)
            .arg(self.config.session_ttl_secs)
            .cmd("EXPIRE")
            .arg(&old_redis_key)
            .arg(5) // [COMMENT]: Hardcode grace period 5 giây để giải phóng nhanh RAM và nâng cao bảo mật (thu hẹp replay attack window).
            .cmd("SADD")
            .arg(&index_key)
            .arg(new_access_key)
            .cmd("SREM")
            .arg(&index_key)
            .arg(old_access_key)
            .cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("Rotation pipeline failed: {}", e)))?;

        // Giải phóng lock sớm (không cần đợi hết 5s)
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }

    // [COMMENT]: Thực hiện giảm TTL của session xuống còn 5 giây thay vì xoá ngay lập tức (DEL)
    // để tránh gây lỗi 401 bất ngờ cho các request song song khác đang bay lơ lửng,
    // đồng thời loại bỏ access key khỏi user index để ngắt liên kết.
    pub async fn delete_session(&self, user_id: &str, access_key: &str) -> Result<(), AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:user_access_session:{}:{}", user_id, access_key);
        let index_key = format!("iam:user_access_index:{}", user_id);

        // Sử dụng pipeline để đảm bảo tính nguyên tử khi cập nhật thông tin phiên
        redis::pipe()
            .atomic()
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .cmd("SREM")
            .arg(&index_key)
            .arg(access_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("Expire session in Redis failed: {}", e)))?;

        Ok(())
    }

    // [COMMENT]: Lấy dữ liệu phục hồi session đã được cache từ Redis L2
    pub async fn get_recovery_cache(
        &self,
        token_hash: &str,
    ) -> Result<Option<RecoverySessionCache>, AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);

        let data: Option<String> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("GET recovery cache failed: {}", e)))?;

        match data {
            Some(json_str) => {
                let cache = serde_json::from_str(&json_str).map_err(|e| {
                    AclError::Internal(format!("JSON deserialize recovery cache failed: {}", e))
                })?;
                Ok(Some(cache))
            }
            None => Ok(None),
        }
    }

    // [COMMENT]: Cố gắng giành quyền Lock phục hồi session bằng SETNX (TTL 5 giây)
    pub async fn try_lock_recovery(&self, token_hash: &str) -> Result<bool, AclError> {
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
            .map_err(|e| AclError::RedisError(format!("Recovery lock acquisition error: {}", e)))?;

        Ok(acquired)
    }

    // [COMMENT]: Lưu kết quả phục hồi session vào Redis L2 và giải phóng lock
    pub async fn set_recovery_cache(
        &self,
        token_hash: &str,
        cache: &RecoverySessionCache,
    ) -> Result<(), AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let json_str = serde_json::to_string(cache).map_err(|e| {
            AclError::Internal(format!("JSON serialize recovery cache failed: {}", e))
        })?;

        // Lưu kết quả cache với TTL ngắn (5 giây) để phục vụ các request đồng thời khác
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
                AclError::RedisError(format!("Set recovery cache in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // [COMMENT]: Kiểm tra xem Lock phục hồi còn tồn tại không
    pub async fn is_recovery_locked(&self, token_hash: &str) -> Result<bool, AclError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let exists: isize = redis::cmd("EXISTS")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("EXISTS lock check failed: {}", e)))?;

        Ok(exists > 0)
    }

    // [COMMENT]: Giải phóng lock recovery session trong Redis L2
    pub async fn release_recovery_lock(&self, token_hash: &str) -> Result<(), AclError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("DEL recovery lock failed: {}", e)))?;
        Ok(())
    }

    // Hỗ trợ kết nối bất đồng bộ sang Redis
    async fn get_connection(&self) -> Result<redis::aio::Connection, AclError> {
        self.redis_client
            .get_tokio_connection()
            .await
            .map_err(|e| AclError::RedisError(format!("Failed to get Redis connection: {}", e)))
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
