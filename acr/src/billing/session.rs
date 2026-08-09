// ======================================================================================================
// 📂 billing/session.rs — Opaque Cost Console alias trỏ về IAM session gốc
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use prost::Message;
use tonic::Status;
use uuid::Uuid;

#[derive(Clone, PartialEq, Message)]
pub struct BillingSessionAlias {
    #[prost(string, tag = "1")]
    pub user_id: String,
    #[prost(string, tag = "2")]
    pub username: String,
    #[prost(string, tag = "3")]
    pub zone_id: String,
    #[prost(string, tag = "4")]
    pub tenant_id: String,
    #[prost(string, tag = "5")]
    pub source_access_key: String,
    #[prost(string, tag = "6")]
    pub source_proof_public_key: String,
    #[prost(string, tag = "7")]
    pub client_proof_public_key: String,
    #[prost(string, tag = "8")]
    pub access_secret_hash: String,
    #[prost(int64, tag = "9")]
    pub created_at: i64,
}

pub struct ReleaseBillingAliasResult {
    pub alias_id: String,
    pub alias_secret: String,
}

pub struct ReleaseBillingAliasCommand<'a> {
    pub user_id: &'a str,
    pub username: &'a str,
    pub zone_id: &'a str,
    pub tenant_id: &'a str,
    pub source_access_key: &'a str,
    pub source_proof_public_key: &'a str,
    pub client_proof_public_key: &'a str,
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    format!("{:x}", Sha256::digest(secret.as_bytes()))
}

impl SessionManager {
    /// [COMMENT]: Alias và reverse index cùng commit trong Redis Security-State để source revoke không bỏ sót Cost.
    pub async fn register_billing_alias(
        &self,
        alias: &BillingSessionAlias,
        alias_id: &str,
    ) -> Result<(), AcrError> {
        let mut encoded = Vec::new();
        alias.encode(&mut encoded).map_err(|error| {
            AcrError::Internal(format!("Failed to encode billing session alias: {error}"))
        })?;

        let mut conn = self.get_connection().await?;
        let alias_key = format!("iam:domain_alias:billing:{alias_id}");
        let source_index = format!("iam:session_alias_index:{}", alias.source_access_key);
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&alias_key)
            .arg(encoded)
            .cmd("EXPIRE")
            .arg(&alias_key)
            .arg(self.config.session_ttl_secs)
            .cmd("SADD")
            .arg(&source_index)
            .arg(&alias_key)
            .cmd("EXPIRE")
            .arg(&source_index)
            .arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Failed to register billing session alias: {error}"))
            })?;

        Logger::sys_info(
            "billing.session_alias",
            &format!("Registered Cost alias for user={}", alias.user_id),
        );
        Ok(())
    }

    pub async fn get_billing_alias(
        &self,
        alias_id: &str,
    ) -> Result<Option<BillingSessionAlias>, AcrError> {
        let mut conn = self.get_connection().await?;
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(format!("iam:domain_alias:billing:{alias_id}"))
            .query_async(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("GET billing session alias failed: {error}"))
            })?;

        data.map(|bytes| {
            BillingSessionAlias::decode(bytes.as_slice()).map_err(|error| {
                AcrError::Internal(format!("Failed to decode billing session alias: {error}"))
            })
        })
        .transpose()
    }

    /// [COMMENT]: Grace TTL giữ request đang in-flight, nhưng alias bị loại khỏi source index ngay.
    pub async fn delete_billing_alias(
        &self,
        alias_id: &str,
        source_access_key: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let alias_key = format!("iam:domain_alias:billing:{alias_id}");
        redis::pipe()
            .atomic()
            .cmd("EXPIRE")
            .arg(&alias_key)
            .arg(5)
            .cmd("SREM")
            .arg(format!("iam:session_alias_index:{source_access_key}"))
            .arg(&alias_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Expire billing session alias failed: {error}"))
            })
    }

    /// [COMMENT]: Logout/device revoke của IAM session gốc thu hồi mọi domain alias cùng family.
    pub async fn revoke_session_aliases(&self, source_access_key: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let source_index = format!("iam:session_alias_index:{source_access_key}");
        let alias_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&source_index)
            .query_async(&mut conn)
            .await
            .map_err(|error| {
                AcrError::RedisError(format!("Read session alias index failed: {error}"))
            })?;

        let mut pipe = redis::pipe();
        pipe.atomic();
        for alias_key in alias_keys {
            pipe.cmd("EXPIRE").arg(alias_key).arg(5);
        }
        pipe.cmd("DEL").arg(source_index);
        pipe.query_async::<_, ()>(&mut conn).await.map_err(|error| {
            AcrError::RedisError(format!("Revoke session aliases failed: {error}"))
        })
    }
}

/// [COMMENT]: Cost chỉ nhận opaque alias; identity và authorization không được copy vào JWT/domain snapshot.
pub async fn release_billing_alias(
    session_mgr: &std::sync::Arc<SessionManager>,
    command: ReleaseBillingAliasCommand<'_>,
) -> Result<ReleaseBillingAliasResult, Status> {
    let ReleaseBillingAliasCommand {
        user_id,
        username,
        zone_id,
        tenant_id,
        source_access_key,
        source_proof_public_key,
        client_proof_public_key,
    } = command;
    let alias_id = Uuid::now_v7().to_string();
    let alias_secret = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
    let alias = BillingSessionAlias {
        user_id: user_id.to_string(),
        username: username.to_string(),
        zone_id: zone_id.to_string(),
        tenant_id: tenant_id.to_string(),
        source_access_key: source_access_key.to_string(),
        source_proof_public_key: source_proof_public_key.to_string(),
        client_proof_public_key: client_proof_public_key.to_string(),
        access_secret_hash: sha256_hash(&alias_secret),
        created_at: chrono::Utc::now().timestamp(),
    };
    session_mgr
        .register_billing_alias(&alias, &alias_id)
        .await
        .map_err(|error| Status::internal(format!("Failed to save billing alias: {error}")))?;

    Ok(ReleaseBillingAliasResult {
        alias_id,
        alias_secret,
    })
}
