// ======================================================================================================
// 📂 infra/redis.rs — SessionManager dùng chung cho toàn bộ domain (User, SRE, Billing)
//
// 📌 VAI TRÒ:
//   - Quản lý kết nối Redis L2 (HA) dùng chung cho mọi domain session.
//   - Cung cấp get_connection() làm entry-point cho mọi Security-State Redis op.
//   - RecoverySessionCache: struct dùng khi session recovery thành công.
//   - Các impl cụ thể (user, admin, billing session ops) được viết trong từng domain file.
// ======================================================================================================

use crate::config::{Config, RedisMode};
use crate::error::AcrError;
use crate::infra::vault::VaultClient;
use redis::aio::ConnectionLike;
use redis::{Cmd, RedisFuture, Value};
use std::sync::Arc;

pub const AUTH_STATE_CONNECTION_PATH: &str =
    "secret/data/connections/redis/auth-state/role-session-rw";
pub const SHARED_L2_CONNECTION_PATH: &str =
    "secret/data/connections/redis/shared-l2/role-auth-request-rw";

pub async fn client_from_vault_with_mode(
    vault: &VaultClient,
    path: &str,
    mode: RedisMode,
) -> Result<Arc<RedisRuntimeClient>, AcrError> {
    let url = vault.read_redis_url(path).await?;
    RedisRuntimeClient::new(&url, mode).map(Arc::new)
}

/// Connection adapter keeps topology at the infra boundary. Cluster commands
/// are slot-routed; Pub/Sub deliberately uses the configured seed connection.
pub enum RedisRuntimeClient {
    Single(redis::Client),
    Cluster {
        client: redis::cluster::ClusterClient,
        pubsub_client: redis::Client,
    },
}

pub enum RedisConnection {
    Single(redis::aio::Connection),
    Cluster(redis::cluster_async::ClusterConnection),
}

impl RedisRuntimeClient {
    pub fn new(url: &str, mode: RedisMode) -> Result<Self, AcrError> {
        let pubsub_client = redis::Client::open(url)
            .map_err(|error| AcrError::RedisError(format!("open Vault Redis client: {error}")))?;
        match mode {
            RedisMode::Single => Ok(Self::Single(pubsub_client)),
            RedisMode::Cluster => {
                let client = redis::cluster::ClusterClient::new([url]).map_err(|error| {
                    AcrError::RedisError(format!("open Vault Redis Cluster client: {error}"))
                })?;
                Ok(Self::Cluster {
                    client,
                    pubsub_client,
                })
            }
        }
    }

    pub async fn get_connection(&self) -> Result<RedisConnection, AcrError> {
        match self {
            Self::Single(client) => client
                .get_tokio_connection()
                .await
                .map(RedisConnection::Single),
            Self::Cluster { client, .. } => client
                .get_async_connection()
                .await
                .map(RedisConnection::Cluster),
        }
        .map_err(|error| AcrError::RedisError(format!("open Redis connection: {error}")))
    }

    pub async fn get_async_connection(&self) -> Result<RedisConnection, AcrError> {
        self.get_connection().await
    }

    pub async fn get_multiplexed_tokio_connection(&self) -> Result<RedisConnection, AcrError> {
        self.get_connection().await
    }

    pub async fn get_pubsub_connection(&self) -> Result<redis::aio::Connection, AcrError> {
        let client = match self {
            Self::Single(client) => client,
            Self::Cluster { pubsub_client, .. } => pubsub_client,
        };
        client.get_async_connection().await.map_err(|error| {
            AcrError::RedisError(format!("open Redis Pub/Sub connection: {error}"))
        })
    }
}

impl ConnectionLike for RedisConnection {
    fn req_packed_command<'a>(&'a mut self, cmd: &'a Cmd) -> RedisFuture<'a, Value> {
        match self {
            Self::Single(connection) => connection.req_packed_command(cmd),
            Self::Cluster(connection) => connection.req_packed_command(cmd),
        }
    }

    fn req_packed_commands<'a>(
        &'a mut self,
        pipeline: &'a redis::Pipeline,
        offset: usize,
        count: usize,
    ) -> RedisFuture<'a, Vec<Value>> {
        match self {
            Self::Single(connection) => connection.req_packed_commands(pipeline, offset, count),
            Self::Cluster(connection) => connection.req_packed_commands(pipeline, offset, count),
        }
    }

    fn get_db(&self) -> i64 {
        match self {
            Self::Single(connection) => connection.get_db(),
            Self::Cluster(connection) => connection.get_db(),
        }
    }
}

/// [COMMENT]: SessionManager — quản lý kết nối Redis L2 tập trung.
/// Được chia sẻ qua Arc<SessionManager> giữa tất cả domain (user, sre, billing).
pub struct SessionManager {
    pub(crate) redis_client: Arc<RedisRuntimeClient>,
    pub(crate) config: Config,
}

impl SessionManager {
    /// [COMMENT]: Khởi tạo SessionManager với Redis client và config.
    pub fn new(redis_client: Arc<RedisRuntimeClient>, config: Config) -> Self {
        Self {
            redis_client,
            config,
        }
    }

    /// [COMMENT]: Lấy kết nối async từ Redis pool (tokio connection).
    pub(crate) async fn get_connection(&self) -> Result<RedisConnection, AcrError> {
        self.redis_client.get_connection().await
    }
}

/// [COMMENT]: RecoverySessionCache — thông tin session phục hồi thành công.
/// Dùng để trao đổi dữ liệu giữa recovery_session và các handler khác.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct RecoverySessionCache {
    pub user_id: String,
    pub client_device_id: String,
    pub level: i32,
    pub tenant_id: String,
    pub new_jwt: String,
    pub new_access_key: String,
    pub new_access_secret: String,
    pub zone_id: String,
    pub zone_code: String,
    // A tenant recovery may fall back to personal only after CP independently
    // authorizes that context. Every ACR replica must then clear stale tenant state.
    pub context_reset: bool,
}
