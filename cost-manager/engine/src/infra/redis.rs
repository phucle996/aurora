use crate::config::Config;
use crate::infra::vault::VaultClient;
use redis::aio::MultiplexedConnection;
use std::error::Error;

const CONNECTION_PATH: &str = "secret/data/connections/redis/engine/role-checkpoint-lock-rw";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    url: String,
}

pub async fn resolve_from_vault(vault: &VaultClient, config: &mut Config) -> Result<(), String> {
    let record: ConnectionRecord = vault.read(CONNECTION_PATH).await?;
    if record.schema_version != 1
        || (!record.url.starts_with("redis://") && !record.url.starts_with("rediss://"))
    {
        return Err("invalid Vault Redis connection record".to_owned());
    }
    config.redis_url = record.url;
    Ok(())
}

// [COMMENT]: Khởi tạo kết nối Redis Multiplexed Connection.
// TLS termination được xử lý ở tầng hạ tầng (service mesh / mTLS proxy) nên
// application chỉ cần kết nối plain TCP tới Redis local endpoint.
pub async fn init_redis_conn(
    vault: &VaultClient,
    config: &Config,
) -> Result<MultiplexedConnection, Box<dyn Error>> {
    let redis_url = if config.redis_url.is_empty() {
        let record: ConnectionRecord = vault
            .read(CONNECTION_PATH)
            .await
            .map_err(std::io::Error::other)?;
        if record.schema_version != 1
            || (!record.url.starts_with("redis://") && !record.url.starts_with("rediss://"))
        {
            return Err(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "invalid Vault Redis connection record",
            )
            .into());
        }
        record.url
    } else {
        config.redis_url.clone()
    };
    let redis_client = redis::Client::open(redis_url)?;

    // [COMMENT]: Khởi tạo một multiplexed connection để các task có thể dùng chung connection Redis một cách an toàn
    let redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    Ok(redis_conn)
}
