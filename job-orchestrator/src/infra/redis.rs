use crate::config::{SharedRedisConfig, TlsTrustSource};
use crate::infra::vault::VaultClient;
use redis::aio::{ConnectionManager, ConnectionManagerConfig, MultiplexedConnection};
use redis::{AsyncConnectionConfig, Client, ClientTlsConfig, TlsCertificates};
use std::fs;
use std::io;
use std::time::Duration;

const CONNECTION_PATH: &str = "secret/data/connections/redis/shared-l2/role-runtime-bridge-rw";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    url: String,
}

pub async fn resolve_from_vault(
    vault: &VaultClient,
    config: &mut SharedRedisConfig,
) -> Result<(), String> {
    let record: ConnectionRecord = vault.read(CONNECTION_PATH).await?;
    if record.schema_version != 1 {
        return Err(format!(
            "unsupported Vault Shared Redis schema_version {}",
            record.schema_version
        ));
    }
    if !record.url.starts_with("redis://") && !record.url.starts_with("rediss://") {
        return Err("Vault Shared Redis URL must use redis:// or rediss://".to_owned());
    }
    config.url = record.url;
    Ok(())
}

pub fn client(config: &SharedRedisConfig) -> io::Result<Client> {
    let Some(tls) = &config.tls else {
        return Client::open(config.url.clone()).map_err(io::Error::other);
    };

    let root_cert = match tls.trust_source {
        TlsTrustSource::System => None,
        TlsTrustSource::File => Some(fs::read(tls.ca_cert.as_ref().ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidInput,
                "Shared Redis file trust requires a CA path",
            )
        })?)?),
    };
    let client_tls = match (&tls.client_cert, &tls.client_key) {
        (Some(cert), Some(key)) => Some(ClientTlsConfig {
            client_cert: fs::read(cert)?,
            client_key: fs::read(key)?,
        }),
        (None, None) => None,
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "Shared Redis client certificate and key must be configured together",
            ))
        }
    };
    Client::build_with_tls(
        config.url.clone(),
        TlsCertificates {
            client_tls,
            root_cert,
        },
    )
    .map_err(io::Error::other)
}

pub async fn multiplexed(
    client: &Client,
    config: &SharedRedisConfig,
) -> redis::RedisResult<MultiplexedConnection> {
    let connection_config = AsyncConnectionConfig::new()
        .set_response_timeout(Duration::from_millis(config.response_timeout_ms))
        .set_connection_timeout(Duration::from_millis(config.connect_timeout_ms));
    client
        .get_multiplexed_async_connection_with_config(&connection_config)
        .await
}

pub async fn manager(
    client: &Client,
    config: &SharedRedisConfig,
) -> redis::RedisResult<ConnectionManager> {
    let manager_config = ConnectionManagerConfig::new()
        .set_exponent_base(config.reconnect_base)
        .set_factor(config.reconnect_factor_ms)
        .set_number_of_retries(config.reconnect_retries)
        .set_response_timeout(Duration::from_millis(config.response_timeout_ms))
        .set_connection_timeout(Duration::from_millis(config.connect_timeout_ms));
    ConnectionManager::new_with_config(client.clone(), manager_config).await
}
