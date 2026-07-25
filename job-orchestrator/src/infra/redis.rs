use crate::config::{SharedRedisConfig, TlsTrustSource};
use redis::aio::{ConnectionManager, MultiplexedConnection};
use redis::{Client, ClientTlsConfig, TlsCertificates};
use std::fs;
use std::io;
use std::time::Duration;

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
