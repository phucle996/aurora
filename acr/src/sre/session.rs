// ======================================================================================================
// 📂 sre/session.rs — SreAccessSession Protobuf + Redis L2 ops + release_sre_session
//
// 📌 NAMESPACE: iam:sre_access_session:{access_key}  (Bỏ zone_id theo kiến trúc HA)
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use prost::Message;
use tonic::Status;
use uuid::Uuid;

/// [COMMENT]: SreAccessSession — Protobuf struct cho SRE.
/// Chứa access_secret_hash và device_public_key Ed25519 để verify signature critical ops.
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct SreAccessSession {
    // Access Secret Hash (SHA-256 của access_secret thô)
    #[prost(string, tag = "1")]
    pub access_secret_hash: ::prost::alloc::string::String,

    // Khóa công khai Ed25519 của thiết bị — dùng để verify chữ ký critical API calls
    #[prost(string, tag = "2")]
    pub device_public_key: ::prost::alloc::string::String,
}

/// [COMMENT]: ReleaseSreSessionResult — kết quả cấp phát Trinity Session SRE
pub struct ReleaseSreSessionResult {
    pub access_token: String,
    pub access_key: String,
    pub access_secret: String,
}

// ─── SessionManager impl for SRE ────────────────────────────────────────

impl SessionManager {
    /// [COMMENT]: Lấy thông tin SRE Session từ Redis L2 namespace iam:sre_access_session:{access_key}
    pub async fn get_sre_session(
        &self,
        access_key: &str,
    ) -> Result<Option<SreAccessSession>, AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:sre_access_session:{}", access_key);

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET SRE session failed: {}", e)))?;

        match data {
            Some(bytes) => {
                let session = SreAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AcrError::Internal(format!("Protobuf decode SreAccessSession failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    /// [COMMENT]: Đăng ký Session SRE mới vào Redis L2, kèm device_public_key Ed25519
    pub async fn register_sre_session(
        &self,
        access_key: &str,
        access_secret_hash: &str,
        device_public_key: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:sre_access_session:{}", access_key);

        let session = SreAccessSession {
            access_secret_hash: access_secret_hash.to_string(),
            device_public_key: device_public_key.to_string(),
        };

        let mut buf = Vec::new();
        session.encode(&mut buf).map_err(|e| {
            AcrError::Internal(format!("Protobuf encode SreAccessSession failed: {}", e))
        })?;

        redis::pipe()
            .atomic()
            .cmd("SET").arg(&redis_key).arg(&buf)
            .cmd("EXPIRE").arg(&redis_key).arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Register SRE session in Redis failed: {}", e))
            })?;

        Ok(())
    }

    /// [COMMENT]: Trinity Rotation SRE — chống Race Condition bằng SETNX Distributed Lock (TTL 5s)
    pub async fn try_rotate_sre_session(
        &self,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
    ) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:sre_refresh:{}", old_access_key);

        // [COMMENT]: 1. Cố gắng giành Lock bằng SET NX — TTL 5s để tự giải phóng khi crash
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key).arg(1)
            .arg("EX").arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("SRE lock acquisition error: {}", e)))?;

        if !acquired {
            // [COMMENT]: Lock bị chiếm bởi request khác — cho request này tiếp tục với session cũ
            return Ok(false);
        }

        // [COMMENT]: 2. Đọc session cũ để giữ lại device_public_key
        let device_public_key = match self.get_sre_session(old_access_key).await? {
            Some(old_sess) => old_sess.device_public_key,
            None => "".to_string(),
        };

        let old_redis_key = format!("iam:sre_access_session:{}", old_access_key);
        let new_redis_key = format!("iam:sre_access_session:{}", new_access_key);

        let new_session = SreAccessSession {
            access_secret_hash: new_ash.to_string(),
            device_public_key,
        };

        let mut buf = Vec::new();
        new_session.encode(&mut buf).map_err(|e| AcrError::Internal(e.to_string()))?;

        // [COMMENT]: 3. Pipeline nguyên tử:
        //   - Tạo session mới đầy đủ TTL
        //   - Giảm TTL session cũ về 5s (Grace Period) — tránh 401 cho requests song song
        redis::pipe()
            .atomic()
            .cmd("SET").arg(&new_redis_key).arg(&buf)
            .cmd("EXPIRE").arg(&new_redis_key).arg(self.config.session_ttl_secs)
            .cmd("EXPIRE").arg(&old_redis_key).arg(5) // grace period
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("SRE rotation pipeline failed: {}", e)))?;

        // [COMMENT]: 4. Giải phóng lock sớm sau khi pipeline thành công
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }

    /// [COMMENT]: Thu hồi SRE Session — giảm TTL về 5s (Grace Period) thay vì DEL ngay
    pub async fn delete_sre_session(&self, access_key: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:sre_access_session:{}", access_key);

        redis::cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Expire SRE session in Redis failed: {}", e))
            })?;

        Ok(())
    }
}

// ─── release_sre_session ────────────────────────────────────────────────────

/// [COMMENT]: Cấp phát Trinity Session cho SRE.
pub async fn release_sre_session(
    session_mgr: &std::sync::Arc<SessionManager>,
    token_mgr: &std::sync::Arc<crate::sre::claims::SreTokenManager>,
    config: &crate::config::Config,
    device_public_key: &str,
) -> Result<ReleaseSreSessionResult, Status> {
    Logger::sys_info("sre.session.release", "Releasing SRE session");

    // 1. Sinh Access Key (UUIDv4) và Access Secret (UUIDv4)
    let access_key = Uuid::new_v4().to_string();
    let access_secret = Uuid::new_v4().to_string();
    let ash = sha256_hash(&access_secret);

    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;

    // 2. Chuẩn bị Claims — SRE: sub="sre", zone_id="global", không có tenant
    let claims = crate::sre::claims::SreClaims {
        sub: "sre".to_string(),
        zone_id: Some("global".to_string()),
        access_key: access_key.clone(),
        jti: Uuid::new_v4().to_string(),
        iss: Some("aurora-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    // 3. Ký JWT qua Vault Transit Engine
    let access_token = match token_mgr.generate_token(&claims).await {
        Ok(t) => t,
        Err(e) => {
            Logger::sys_error(
                "sre.session.release",
                "Vault JWT signing failed for SRE",
                &e.to_string(),
            );
            return Err(Status::internal("Failed to issue session token"));
        }
    };

    // 4. Validate device_public_key nếu được cung cấp (phải là Ed25519 32-byte base64)
    if !device_public_key.is_empty() {
        use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
        let is_valid = match BASE64.decode(device_public_key) {
            Ok(bytes) => bytes.len() == 32,
            Err(_) => false,
        };
        if !is_valid {
            Logger::sys_warn(
                "sre.session.release",
                "SRE login failed: Invalid device_public_key format or length",
                "",
            );
            return Err(Status::invalid_argument(
                "Invalid device_public_key format or length (must be a valid 32-byte Base64-encoded key)",
            ));
        }
    }

    // 5. Đăng ký SRE Session vào Redis L2
    if let Err(e) = session_mgr
        .register_sre_session(&access_key, &ash, device_public_key)
        .await
    {
        Logger::sys_error(
            "sre.session.release",
            "Redis SRE session registration failed",
            &e.to_string(),
        );
        return Err(Status::internal("Failed to save session state"));
    }

    Ok(ReleaseSreSessionResult {
        access_token,
        access_key,
        access_secret,
    })
}

// ─── Helper ────────────────────────────────────────────────────────────────────

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
