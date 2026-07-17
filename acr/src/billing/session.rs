// ======================================================================================================
// 📂 billing/session.rs — Quản lý session trong Redis cho Billing Auditor
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use tonic::Status;
use uuid::Uuid;

// Import protobuf sinh ra từ proto/billing_auth.proto
#[allow(dead_code)]
pub mod billing_proto {
    tonic::include_proto!("billing.rpc");
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct BillingAccessSession {
    pub user_id: String,
    pub employee_code: String,
    pub role_id: String,
    pub level: i32,
    pub access_secret_hash: String,
    pub created_at: i64,
}

pub struct ReleaseBillingSessionResult {
    pub access_token: String,
    pub access_key: String,
    pub access_secret: String,
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

impl SessionManager {
    /// [COMMENT]: Đăng ký session mới cho Billing Auditor vào Redis L2.
    /// TTL của session được cấu hình qua Config (mặc định 2 tiếng).
    pub async fn register_billing_session(
        &self,
        user_id: &str,
        employee_code: &str,
        role_id: &str,
        level: i32,
        access_key: &str,
        access_secret_hash: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;

        // 1. Khởi tạo BillingAccessSession struct
        let session = BillingAccessSession {
            user_id: user_id.to_string(),
            employee_code: employee_code.to_string(),
            role_id: role_id.to_string(),
            level,
            access_secret_hash: access_secret_hash.to_string(),
            created_at: chrono::Utc::now().timestamp(),
        };

        // 2. Serialize struct thành bytes JSON
        let bytes = serde_json::to_vec(&session).map_err(|e| {
            AcrError::Internal(format!("Failed to serialize billing session: {}", e))
        })?;

        let redis_key = format!("billing:session:{}", access_key);

        // 3. Ghi atomic vào Redis với EXPIRE (TTL)
        let mut pipe = redis::pipe();
        pipe.atomic();
        pipe.cmd("SET").arg(&redis_key).arg(bytes);
        pipe.cmd("EXPIRE")
            .arg(&redis_key)
            .arg(self.config.session_ttl_secs);

        pipe.query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Failed to save billing session: {}", e)))?;

        Logger::sys_info(
            "billing.session",
            &format!(
                "Successfully registered billing auditor session. key={}, user={}",
                redis_key, user_id
            ),
        );

        Ok(())
    }

    /// [COMMENT]: Lấy thông tin Billing Auditor Session từ Redis L2.
    pub async fn get_billing_session(
        &self,
        access_key: &str,
    ) -> Result<Option<BillingAccessSession>, AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("billing:session:{}", access_key);

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET billing session failed: {}", e)))?;

        match data {
            Some(bytes) => {
                let session = serde_json::from_slice(bytes.as_slice()).map_err(|e| {
                    AcrError::Internal(format!("Failed to decode billing session: {}", e))
                })?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    /// [COMMENT]: Thu hồi Billing Auditor Session bằng cách đặt TTL về 5s thay vì xóa ngay lập tức.
    pub async fn delete_billing_session(&self, access_key: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("billing:session:{}", access_key);

        redis::cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("EXPIRE billing session failed: {}", e)))?;

        Ok(())
    }
}

// ─── release_billing_session ──────────────────────────────────────────────────

/// [COMMENT]: Cấp phát Trinity Session cô lập cho Billing Auditor — không có Tenant hay Device info.
pub async fn release_billing_session(
    session_mgr: &std::sync::Arc<SessionManager>,
    token_mgr: &std::sync::Arc<crate::billing::claims::TokenManager>,
    nats: &Nats,
    redis_client: &redis::Client,
    config: &crate::config::Config,
    user_id: &str,
    employee_code: &str,
    role_id: &str,
    level: i32,
    zone_id: &str,
) -> Result<ReleaseBillingSessionResult, Status> {
    Logger::sys_info(
        "billing.session.release",
        &format!(
            "Releasing new billing auditor session for user_id={}",
            user_id
        ),
    );

    // 1. Sinh Access Key (UUIDv7) và Access Secret (UUIDv4)
    let access_key = Uuid::now_v7().to_string();
    let access_secret = Uuid::new_v4().to_string();
    let ash = sha256_hash(&access_secret);

    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;

    // 2. Chuẩn bị BillingClaims (không có tenant_id)
    let claims = crate::billing::claims::BillingClaims {
        sub: employee_code.to_string(),
        uid: user_id.to_string(),
        role_id: role_id.to_string(),
        lvl: level,
        zone_id: if zone_id.is_empty() {
            None
        } else if Uuid::parse_str(zone_id).is_ok() {
            Some(zone_id.to_string())
        } else {
            crate::infra::zone::resolve_code_to_id_and_status(nats, redis_client, zone_id)
                .await
                .map(|(id, _)| id)
        },
        access_key: access_key.clone(),
        jti: Uuid::new_v4().to_string(),
        iss: Some("aurora-billing-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    // 3. Ký Billing JWT qua Vault Transit Engine
    let access_token = match token_mgr.generate_billing_token(&claims).await {
        Ok(token) => token,
        Err(e) => {
            Logger::sys_error(
                "billing.session.release",
                "Failed to sign billing access token via Vault",
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Failed to sign billing access token: {}",
                e
            )));
        }
    };

    // 4. Đăng ký session vào Redis L2
    if let Err(e) = session_mgr
        .register_billing_session(user_id, employee_code, role_id, level, &access_key, &ash)
        .await
    {
        Logger::sys_error(
            "billing.session.release",
            "Failed to save billing session state to Redis",
            &e.to_string(),
        );
        return Err(Status::internal("Failed to save billing session state"));
    }

    Ok(ReleaseBillingSessionResult {
        access_token,
        access_key,
        access_secret,
    })
}
