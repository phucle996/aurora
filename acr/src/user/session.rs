// ======================================================================================================
// 📂 user/session.rs — UserAccessSession Protobuf + Redis L2 ops cho User domain
//
// 📌 NAMESPACE: iam:user_session:{zone_id}:{tenant_id}:{user_id}:{access_key}
// 📌 INDEX: iam:user_access_index:{user_id} (Set), iam:device_access_index:{device_id} (Set)
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::redis::{RecoverySessionCache, SessionManager};
use prost::Message;

/// [COMMENT]: UserAccessSession — Protobuf struct cho User session.
/// Tương thích 100% với iamproto.UserAccessSession của Go controlplane.
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct UserAccessSession {
    // Access Secret Hash (SHA-256 của access_secret thô)
    #[prost(string, tag = "1")]
    pub ash: ::prost::alloc::string::String,

    // Tracked Device ID (UUID của thiết bị đã đăng nhập)
    #[prost(string, tag = "2")]
    pub tdid: ::prost::alloc::string::String,

    // Last Seen At (Unix timestamp ghi nhận hoạt động cuối)
    #[prost(int64, tag = "3")]
    pub lsa: i64,
}

// ─── SessionManager impl for User Sessions ────────────────────────────────────

impl SessionManager {
    /// [COMMENT]: Lấy User Session từ Redis L2 theo khoá phân cấp.
    pub async fn get_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
    ) -> Result<Option<UserAccessSession>, AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET session failed: {}", e)))?;

        match data {
            Some(bytes) => {
                let session = UserAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AcrError::Internal(format!("Protobuf decode UserAccessSession failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    /// [COMMENT]: Đăng ký User Session mới, kèm index user và device.
    /// Trả về danh sách client_device_ids bị evict khi vượt quá USER_DEVICE_CAP=50.
    pub async fn register_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
        ash: &str,
        device_id: &str,
    ) -> Result<Vec<String>, AcrError> {
        const USER_DEVICE_CAP: usize = 50;

        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let now = chrono::Utc::now().timestamp();
        let session = UserAccessSession {
            ash: ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        let mut buf = Vec::new();
        session.encode(&mut buf)
            .map_err(|e| AcrError::Internal(format!("Protobuf encode UserAccessSession failed: {}", e)))?;

        let index_key = format!("iam:user_access_index:{}", user_id);
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        // [COMMENT]: Lưu full redis key vào index để CP (Go) có thể scan/delete đúng
        redis::pipe()
            .atomic()
            .cmd("SET").arg(&redis_key).arg(&buf)
            .cmd("EXPIRE").arg(&redis_key).arg(self.config.session_ttl_secs)
            .cmd("SADD").arg(&index_key).arg(&redis_key)
            .cmd("EXPIRE").arg(&index_key).arg(self.config.session_ttl_secs * 3)
            .cmd("SADD").arg(&dev_index_key).arg(&redis_key)
            .cmd("EXPIRE").arg(&dev_index_key).arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Register session in Redis failed: {}", e)))?;

        // [COMMENT]: Evict session cũ nếu vượt quá USER_DEVICE_CAP
        let session_count: usize = redis::cmd("SCARD")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(0);

        if session_count <= USER_DEVICE_CAP {
            return Ok(vec![]);
        }

        let all_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .unwrap_or_default();

        if all_keys.is_empty() {
            return Ok(vec![]);
        }

        // [COMMENT]: Pipeline GET toàn bộ session để so sánh lsa
        let mut pipe = redis::pipe();
        for key in &all_keys {
            pipe.cmd("GET").arg(key);
        }
        let datas: Vec<Option<Vec<u8>>> = pipe.query_async(&mut conn).await.unwrap_or_default();

        let mut sessions: Vec<(String, String, i64)> = all_keys
            .into_iter()
            .zip(datas.into_iter())
            .filter_map(|(key, data)| {
                let bytes = data?;
                let s = UserAccessSession::decode(bytes.as_slice()).ok()?;
                Some((key, s.tdid, s.lsa))
            })
            .collect();

        // [COMMENT]: Sort ascending bởi lsa — session cũ nhất evict trước
        sessions.sort_by_key(|(_, _, lsa)| *lsa);

        let excess = session_count.saturating_sub(USER_DEVICE_CAP);
        let to_evict = sessions.into_iter().take(excess).collect::<Vec<_>>();

        if to_evict.is_empty() {
            return Ok(vec![]);
        }

        let mut del_pipe = redis::pipe();
        for (key, _, _) in &to_evict {
            del_pipe.cmd("DEL").arg(key);
            del_pipe.cmd("SREM").arg(&index_key).arg(key);
        }
        let _ = del_pipe.query_async::<_, Vec<redis::Value>>(&mut conn).await;

        let evicted_tdids: Vec<String> = to_evict.into_iter().map(|(_, tdid, _)| tdid).collect();

        Ok(evicted_tdids)
    }

    /// [COMMENT]: Cập nhật Last Seen At — throttle 30s để giảm tải Redis
    pub async fn update_last_seen(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
        last_lsa: i64,
    ) -> Result<(), AcrError> {
        let now = chrono::Utc::now().timestamp();

        // [COMMENT]: Chỉ ghi Redis nếu lệch quá 30s (giảm tải write)
        if now - last_lsa < 30 {
            return Ok(());
        }

        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(e.to_string()))?;

        if let Some(bytes) = data {
            let mut session = UserAccessSession::decode(bytes.as_slice())
                .map_err(|e| AcrError::Internal(e.to_string()))?;

            session.lsa = now;
            let mut buf = Vec::new();
            session.encode(&mut buf).map_err(|e| AcrError::Internal(e.to_string()))?;

            // [COMMENT]: Giữ nguyên TTL hiện tại khi update LSA
            let ttl: u64 = redis::cmd("TTL")
                .arg(&redis_key)
                .query_async(&mut conn)
                .await
                .unwrap_or(self.config.session_ttl_secs);

            redis::pipe()
                .atomic()
                .cmd("SET").arg(&redis_key).arg(&buf)
                .cmd("EXPIRE").arg(&redis_key).arg(ttl)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|e| AcrError::RedisError(format!("Update LSA failed: {}", e)))?;
        }

        Ok(())
    }

    /// [COMMENT]: Trinity Rotation User — SETNX Lock chống Race Condition, Grace Period 5s
    pub async fn try_rotate_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
        device_id: &str,
    ) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:refresh:{}", old_access_key);

        // [COMMENT]: 1. SETNX Lock — TTL 5s auto-release khi crash
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key).arg(1)
            .arg("EX").arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Lock acquisition error: {}", e)))?;

        if !acquired {
            // [COMMENT]: Lock bị chiếm — luồng khác đang rotate — tiếp tục với session cũ
            return Ok(false);
        }

        let old_redis_key = format!("iam:user_session:{}:{}:{}:{}", zone_id, tenant_id, user_id, old_access_key);
        let new_redis_key = format!("iam:user_session:{}:{}:{}:{}", zone_id, tenant_id, user_id, new_access_key);
        let index_key = format!("iam:user_access_index:{}", user_id);
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        let now = chrono::Utc::now().timestamp();
        let new_session = UserAccessSession {
            ash: new_ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        let mut buf = Vec::new();
        new_session.encode(&mut buf).map_err(|e| AcrError::Internal(e.to_string()))?;

        // [COMMENT]: 2. Pipeline nguyên tử:
        //   - SET session mới + EXPIRE đầy đủ TTL
        //   - EXPIRE session cũ về 5s (Grace Period — tránh 401 cho requests bay lơ lửng)
        //   - Cập nhật user index và device index
        redis::pipe()
            .atomic()
            .cmd("SET").arg(&new_redis_key).arg(&buf)
            .cmd("EXPIRE").arg(&new_redis_key).arg(self.config.session_ttl_secs)
            .cmd("EXPIRE").arg(&old_redis_key).arg(5)
            .cmd("SADD").arg(&index_key).arg(&new_redis_key)
            .cmd("SREM").arg(&index_key).arg(&old_redis_key)
            .cmd("EXPIRE").arg(&index_key).arg(self.config.session_ttl_secs * 3)
            .cmd("SADD").arg(&dev_index_key).arg(&new_redis_key)
            .cmd("SREM").arg(&dev_index_key).arg(&old_redis_key)
            .cmd("EXPIRE").arg(&dev_index_key).arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Rotation pipeline failed: {}", e)))?;

        // [COMMENT]: 3. Giải phóng lock sớm (không cần chờ 5s hết hạn)
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }

    /// [COMMENT]: Thu hồi User Session — EXPIRE về 5s (Grace Period) + xoá khỏi indices
    pub async fn delete_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );
        let index_key = format!("iam:user_access_index:{}", user_id);

        // [COMMENT]: Decode session để lấy tdid trước khi xoá
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(None);

        let device_id_opt = data.and_then(|bytes| {
            UserAccessSession::decode(bytes.as_slice())
                .ok()
                .map(|s| s.tdid)
        });

        let mut pipe = redis::pipe();
        pipe.atomic()
            .cmd("EXPIRE").arg(&redis_key).arg(5)
            .cmd("SREM").arg(&index_key).arg(&redis_key);

        if let Some(ref device_id) = device_id_opt {
            let dev_index_key = format!("iam:device_access_index:{}", device_id);
            pipe.cmd("SREM").arg(&dev_index_key).arg(&redis_key);
        }

        pipe.query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Expire session in Redis failed: {}", e)))?;

        Ok(())
    }

    // ─── Recovery Session Helpers ────────────────────────────────────────────

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
            .arg(&lock_key).arg(1)
            .arg("EX").arg(5)
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
            .cmd("SET").arg(&redis_key).arg(&json_str)
            .cmd("EXPIRE").arg(&redis_key).arg(5)
            .cmd("DEL").arg(&lock_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Set recovery cache in Redis failed: {}", e)))?;

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

    /// [COMMENT]: Lấy tất cả session đang hoạt động của user từ user index
    pub async fn get_active_sessions(
        &self,
        user_id: &str,
    ) -> Result<Vec<(String, i64)>, AcrError> {
        let mut conn = self.get_connection().await?;
        let index_key = format!("iam:user_access_index:{}", user_id);

        let session_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("SMEMBERS failed for index {}: {}", index_key, e)))?;

        if session_keys.is_empty() {
            return Ok(vec![]);
        }

        let mut pipe = redis::pipe();
        for key in &session_keys {
            pipe.cmd("GET").arg(key);
        }

        let datas: Vec<Option<Vec<u8>>> = pipe
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("MGET sessions failed: {}", e)))?;

        let mut active_sessions = Vec::new();
        for data in datas.into_iter().flatten() {
            if let Ok(session) = UserAccessSession::decode(data.as_slice()) {
                active_sessions.push((session.tdid, session.lsa));
            }
        }

        Ok(active_sessions)
    }
}
