use crate::config::{NatsAuthMode, NatsCoreConfig, TlsTrustSource};
use async_nats::{Client, ConnectOptions};
use std::io;
use std::time::Duration;

pub async fn connect(config: &NatsCoreConfig) -> io::Result<Client> {
    let mut options = match config.auth_mode {
        NatsAuthMode::None => ConnectOptions::new(),
        NatsAuthMode::Token => ConnectOptions::with_token(
            config
                .token
                .clone()
                .ok_or_else(|| invalid_config("NATS_TOKEN is required"))?,
        ),
        NatsAuthMode::UserPassword => ConnectOptions::with_user_and_password(
            config
                .username
                .clone()
                .ok_or_else(|| invalid_config("NATS_USERNAME is required"))?,
            config
                .password
                .clone()
                .ok_or_else(|| invalid_config("NATS_PASSWORD is required"))?,
        ),
        NatsAuthMode::CredentialsFile => {
            ConnectOptions::with_credentials_file(
                config
                    .credentials_file
                    .as_ref()
                    .ok_or_else(|| invalid_config("NATS_CREDENTIALS_FILE is required"))?,
            )
            .await?
        }
    };

    options = options
        .name(&config.client_name)
        .require_tls(config.tls.is_some())
        .connection_timeout(Duration::from_secs(config.connect_timeout_secs))
        .request_timeout(Some(Duration::from_secs(config.request_timeout_secs)))
        .ping_interval(Duration::from_secs(config.ping_interval_secs))
        .subscription_capacity(config.subscription_capacity)
        .client_capacity(config.client_capacity);
    let reconnect_base = config.reconnect_base_delay_ms;
    let reconnect_max = config.reconnect_max_delay_ms;
    let pod_jitter = crate::config::get_node_hostname()
        .bytes()
        .fold(0_u64, |sum, value| sum.wrapping_add(u64::from(value)))
        % reconnect_base.max(1);
    options = options.reconnect_delay_callback(move |attempts| {
        // A stable pod jitter avoids synchronized reconnect storms while the
        // exponential component remains capped during a long NATS outage.
        let multiplier = 1_u64 << attempts.min(10);
        Duration::from_millis(
            reconnect_base
                .saturating_mul(multiplier)
                .saturating_add(pod_jitter)
                .min(reconnect_max),
        )
    });
    if config.retry_initial_connect {
        options = options.retry_on_initial_connect();
    }
    if config.tls_first {
        options = options.tls_first();
    }
    if let Some(tls) = &config.tls {
        if tls.trust_source == TlsTrustSource::File {
            let ca_cert = tls
                .ca_cert
                .as_ref()
                .ok_or_else(|| invalid_config("NATS file trust requires NATS_TLS_CA_CERT"))?;
            options = options.add_root_certificates(ca_cert.clone());
        }
        match (&tls.client_cert, &tls.client_key) {
            (Some(cert), Some(key)) => {
                options = options.add_client_certificate(cert.clone(), key.clone());
            }
            (None, None) => {}
            _ => {
                return Err(invalid_config(
                    "NATS client certificate and key must be configured together",
                ))
            }
        }
    }

    let servers = config
        .urls
        .iter()
        .map(|url| {
            url.parse::<async_nats::ServerAddr>()
                .map_err(io::Error::other)
        })
        .collect::<io::Result<Vec<_>>>()?;
    options.connect(servers).await.map_err(io::Error::other)
}

fn invalid_config(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}
