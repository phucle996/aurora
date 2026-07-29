use crate::config::{SharedRedisConfig, TlsTrustSource};
use crate::infra::vault::VaultClient;
use redis::aio::{ConnectionManager, MultiplexedConnection};
use redis::{Client, ClientTlsConfig, TlsCertificates};
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
    client
        .get_multiplexed_async_connection_with_timeouts(
            Duration::from_millis(config.response_timeout_ms),
            Duration::from_millis(config.connect_timeout_ms),
        )
        .await
}

pub async fn manager(
    client: &Client,
    config: &SharedRedisConfig,
) -> redis::RedisResult<ConnectionManager> {
    ConnectionManager::new_with_backoff_and_timeouts(
        client.clone(),
        config.reconnect_base,
        config.reconnect_factor_ms,
        config.reconnect_retries,
        Duration::from_millis(config.response_timeout_ms),
        Duration::from_millis(config.connect_timeout_ms),
    )
    .await
}
