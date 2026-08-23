// ======================================================================================================
// 📂 user/session.rs — UserAccessSession Protobuf + Redis L2 ops cho User domain
//
// 📌 NAMESPACE: iam:user_session:{zone_id}:{tenant_id}:{user_id}:{access_key}
// 📌 INDEX: iam:user_access_index:{user_id} (Set), iam:device_access_index:{device_id} (Set)
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::redis::SessionManager;
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

    // [COMMENT]: Canonical Ed25519 public key do IAM trả về sau login, dùng cho session-proof critical routes.
    #[prost(string, tag = "4")]
    pub client_proof_public_key: ::prost::alloc::string::String,
}

pub struct RegisterSessionCommand<'a> {
    pub zone_id: &'a str,
    pub tenant_id: &'a str,
    pub user_id: &'a str,
    pub access_key: &'a str,
    pub access_secret_hash: &'a str,
    pub device_id: &'a str,
    pub client_proof_public_key: &'a str,
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
                let session = UserAccessSession::decode(bytes.as_slice()).map_err(|e| {
                    AcrError::Internal(format!("Protobuf decode UserAccessSession failed: {}", e))
                })?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    /// [COMMENT]: Đăng ký User Session mới, kèm index user và device.
    /// Trả về danh sách client_device_ids bị evict khi vượt quá USER_DEVICE_CAP=50.
    pub async fn register_session(
        &self,
        command: RegisterSessionCommand<'_>,
    ) -> Result<Vec<String>, AcrError> {
        let RegisterSessionCommand {
            zone_id,
            tenant_id,
            user_id,
            access_key,
            access_secret_hash,
            device_id,
            client_proof_public_key,
        } = command;
        const USER_DEVICE_CAP: usize = 50;

        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let now = chrono::Utc::now().timestamp();
        let session = UserAccessSession {
            ash: access_secret_hash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
            client_proof_public_key: client_proof_public_key.to_string(),
        };

        let mut buf = Vec::new();
        session.encode(&mut buf).map_err(|e| {
            AcrError::Internal(format!("Protobuf encode UserAccessSession failed: {}", e))
        })?;

        let index_key = format!("iam:user_access_index:{}", user_id);
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        // The session itself is the authorization authority. Index writes are
        // best-effort discovery data and intentionally remain independent
        // slots so the workflow runs on both single Redis and Redis Cluster.
        redis::cmd("SET")
            .arg(&redis_key)
            .arg(&buf)
            .arg("EX")
            .arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Register session in Redis failed: {}", e))
            })?;
        redis::cmd("SADD")
            .arg(&index_key)
            .arg(&redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Index user session failed: {e}")))?;
        redis::cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Set user index TTL failed: {e}")))?;
        redis::cmd("SADD")
            .arg(&dev_index_key)
            .arg(&redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Index device session failed: {e}")))?;
        redis::cmd("EXPIRE")
            .arg(&dev_index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Set device index TTL failed: {}", e)))?;

        // [COMMENT]: Evict session cũ nếu vượt quá USER_DEVICE_CAP. Không được coi lỗi
        // đọc Auth Redis là count=0, vì như vậy outage sẽ bypass giới hạn thiết bị.
        let session_count: usize = redis::cmd("SCARD")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Read user session count failed: {error}"))
            })?;

        if session_count <= USER_DEVICE_CAP {
            return Ok(vec![]);
        }

        let all_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Read user session index failed: {error}"))
            })?;

        if all_keys.is_empty() {
            return Err(AcrError::RedisError(
                "User session index is empty while SCARD exceeded device cap".to_string(),
            ));
        }

        let mut sessions: Vec<(String, String, i64)> = Vec::with_capacity(all_keys.len());
        for key in all_keys {
            let data: Option<Vec<u8>> = redis::cmd("GET")
                .arg(&key)
                .query_async(&mut conn)
                .await
                .map_err(|error| {
                    AcrError::RedisError(format!("Read user session for eviction failed: {error}"))
                })?;
            let Some(bytes) = data else {
                let _: Result<(), _> = redis::cmd("SREM")
                    .arg(&index_key)
                    .arg(&key)
                    .query_async(&mut conn)
                    .await;
                continue;
            };
            let session = UserAccessSession::decode(bytes.as_slice()).map_err(|error| {
                AcrError::Internal(format!(
                    "Invalid UserAccessSession payload in {key}: {error}"
                ))
            })?;
            sessions.push((key, session.tdid, session.lsa));
        }

        // [COMMENT]: Sort ascending bởi lsa — session cũ nhất evict trước
        sessions.sort_by_key(|(_, _, lsa)| *lsa);

        let excess = session_count.saturating_sub(USER_DEVICE_CAP);
        let to_evict = sessions.into_iter().take(excess).collect::<Vec<_>>();

        if to_evict.is_empty() {
            return Ok(vec![]);
        }

        let evicted_tdids: Vec<String> = to_evict.iter().map(|(_, tdid, _)| tdid.clone()).collect();
        let notification = crate::infra::iam_proto::auth::EvictedDevicesNotification {
            user_id: user_id.to_string(),
            client_device_ids: evicted_tdids.clone(),
        };
        let mut notification_bytes = Vec::with_capacity(notification.encoded_len());
        notification
            .encode(&mut notification_bytes)
            .map_err(|error| {
                AcrError::Internal(format!(
                    "Protobuf encode EvictedDevicesNotification failed: {error}"
                ))
            })?;

        // Persist the outbox before destructive cleanup. Replays are safe and
        // are preferable to silently losing a device eviction on a crash.
        redis::cmd("XADD")
            .arg("iam:device:eviction-outbox")
            .arg("*")
            .arg("payload")
            .arg(notification_bytes)
            .query_async::<_, String>(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Append device eviction outbox failed: {error}"))
            })?;
        for (key, tdid, _) in &to_evict {
            let _: () = redis::cmd("DEL")
                .arg(key)
                .query_async(&mut conn)
                .await
                .map_err(|error| {
                    AcrError::RedisError(format!("Delete evicted session failed: {error}"))
                })?;
            let _: () = redis::cmd("SREM")
                .arg(&index_key)
                .arg(key)
                .query_async(&mut conn)
                .await
                .map_err(|error| {
                    AcrError::RedisError(format!("Remove evicted user index failed: {error}"))
                })?;
            let _: () = redis::cmd("SREM")
                .arg(format!("iam:device_access_index:{tdid}"))
                .arg(key)
                .query_async(&mut conn)
                .await
                .map_err(|error| {
                    AcrError::RedisError(format!("Remove evicted device index failed: {error}"))
                })?;
        }

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
            session
                .encode(&mut buf)
                .map_err(|e| AcrError::Internal(e.to_string()))?;

            // [COMMENT]: Giữ nguyên TTL hiện tại khi update LSA
            let ttl: u64 = redis::cmd("TTL")
                .arg(&redis_key)
                .query_async(&mut conn)
                .await
                .unwrap_or(self.config.session_ttl_secs);

            redis::cmd("SET")
                .arg(&redis_key)
                .arg(&buf)
                .arg("EX")
                .arg(ttl)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|e| AcrError::RedisError(format!("Update LSA failed: {}", e)))?;
        }

        Ok(())
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

        redis::cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Expire session in Redis failed: {}", e)))?;
        redis::cmd("SREM")
            .arg(&index_key)
            .arg(&redis_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Remove user session index failed: {}", e))
            })?;

        if let Some(ref device_id) = device_id_opt {
            let dev_index_key = format!("iam:device_access_index:{}", device_id);
            redis::cmd("SREM")
                .arg(&dev_index_key)
                .arg(&redis_key)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|e| {
                    AcrError::RedisError(format!("Remove device session index failed: {}", e))
                })?;
        }

        Ok(())
    }

    /// [COMMENT]: Lấy tất cả session đang hoạt động của user từ user index
    pub async fn get_active_sessions(&self, user_id: &str) -> Result<Vec<(String, i64)>, AcrError> {
        let mut conn = self.get_connection().await?;
        let index_key = format!("iam:user_access_index:{}", user_id);

        let session_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("SMEMBERS failed for index {}: {}", index_key, e))
            })?;

        if session_keys.is_empty() {
            return Ok(vec![]);
        }

        let mut active_sessions = Vec::new();
        for key in session_keys {
            let data: Option<Vec<u8>> = redis::cmd("GET")
                .arg(key)
                .query_async(&mut conn)
                .await
                .map_err(|e| AcrError::RedisError(format!("Read active session failed: {}", e)))?;
            let Some(data) = data else { continue };
            if let Ok(session) = UserAccessSession::decode(data.as_slice()) {
                active_sessions.push((session.tdid, session.lsa));
            }
        }

        Ok(active_sessions)
    }
}
