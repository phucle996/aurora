use prost::Message;
use std::sync::Arc;
use crate::error::AclError;
use crate::config::Config;

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
        Self { redis_client, config }
    }

    // Lấy thông tin Session từ Redis L2
    pub async fn get_session(&self, user_id: &str, access_key: &str) -> Result<Option<UserAccessSession>, AclError> {
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
        session.encode(&mut buf)
            .map_err(|e| AclError::Internal(format!("Protobuf encode failed: {}", e)))?;

        // Ghi session kèm thời gian sống (TTL)
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("Register session in Redis failed: {}", e)))?;

        Ok(())
    }

    // Throttled update cho Last Seen At (Giảm tải số lệnh ghi vào Redis L2)
    pub async fn update_last_seen(&self, user_id: &str, access_key: &str, last_lsa: i64) -> Result<(), AclError> {
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
            session.encode(&mut buf)
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

        let now = chrono::Utc::now().timestamp();
        let new_session = UserAccessSession {
            ash: new_ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        let mut buf = Vec::new();
        new_session.encode(&mut buf)
            .map_err(|e| AclError::Internal(e.to_string()))?;

        // 3. Thực thi Pipeline nguyên tử:
        // - Tạo session mới với đầy đủ TTL
        // - Chuyển session cũ sang thời gian sống ngắn hạn (Grace Period - 15 giây) để tránh lỗi 401 cho các request đang bay lơ lửng
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
            .arg(self.config.grace_period_secs)
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

    // [COMMENT]: Thực hiện xoá session runtime tại L2 Redis và loại bỏ access key khỏi user index.
    // Phương thức này được thực thi đồng bộ và trả lỗi ngay lập tức nếu Redis gặp sự cố.
    pub async fn delete_session(&self, user_id: &str, access_key: &str) -> Result<(), AclError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:user_access_session:{}:{}", user_id, access_key);
        let index_key = format!("iam:user_access_index:{}", user_id);

        // Sử dụng pipeline để đảm bảo tính nguyên tử khi xóa thông tin phiên
        redis::pipe()
            .atomic()
            .cmd("DEL")
            .arg(&redis_key)
            .cmd("SREM")
            .arg(&index_key)
            .arg(access_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("Delete session in Redis failed: {}", e)))?;

        Ok(())
    }

    // Hỗ trợ kết nối bất đồng bộ sang Redis
    async fn get_connection(&self) -> Result<redis::aio::Connection, AclError> {
        self.redis_client.get_tokio_connection().await
            .map_err(|e| AclError::RedisError(format!("Failed to get Redis connection: {}", e)))
    }
}
