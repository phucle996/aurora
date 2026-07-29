use crate::config::{NatsAuthMode, NatsCoreConfig, TlsClientConfig, TlsTrustSource};
use crate::infra::vault::VaultClient;
use async_nats::{Client, ConnectOptions};
use std::io;
use std::path::PathBuf;
use std::time::Duration;

const CONNECTION_PATH: &str = "secret/data/connections/nats/central/role-job-orchestrator";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    urls: Vec<String>,
    client_name: String,
    auth_mode: String,
    token: Option<String>,
    username: Option<String>,
    password: Option<String>,
    credentials_file: Option<String>,
    tls_enabled: bool,
    tls_trust_source: Option<String>,
    ca_cert_path: Option<String>,
    client_cert_path: Option<String>,
    client_key_path: Option<String>,
    tls_first: bool,
}

pub async fn resolve_from_vault(
    vault: &VaultClient,
    config: &mut NatsCoreConfig,
) -> Result<(), String> {
    let record: ConnectionRecord = vault.read(CONNECTION_PATH).await?;
    if record.schema_version != 1 {
        return Err(format!(
            "unsupported Vault NATS schema_version {}",
            record.schema_version
        ));
    }
    let urls = record
        .urls
        .iter()
        .map(|url| url.trim())
        .filter(|url| !url.is_empty())
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if urls.is_empty()
        || urls
            .iter()
            .any(|url| !url.starts_with("nats://") && !url.starts_with("tls://"))
    {
        return Err("Vault NATS urls must contain nats:// or tls:// endpoints".to_owned());
    }
    let uses_tls = urls.iter().all(|url| url.starts_with("tls://"));
    if !uses_tls && urls.iter().any(|url| url.starts_with("tls://")) {
        return Err("Vault NATS urls cannot mix TLS and plaintext endpoints".to_owned());
    }
    let auth_mode: NatsAuthMode = record.auth_mode.parse()?;
    let token = record.token.filter(|value| !value.trim().is_empty());
    let username = record.username.filter(|value| !value.trim().is_empty());
    let password = record.password.filter(|value| !value.trim().is_empty());
    let credentials_file = record
        .credentials_file
        .filter(|value| !value.trim().is_empty())
        .map(Into::into);
    validate_auth(auth_mode, &token, &username, &password, &credentials_file)?;
    if auth_mode != NatsAuthMode::None && !uses_tls {
        return Err("Vault NATS authentication requires TLS endpoints".to_owned());
    }

    let has_tls_material = record.tls_trust_source.is_some()
        || record.ca_cert_path.is_some()
        || record.client_cert_path.is_some()
        || record.client_key_path.is_some();
    let tls = if uses_tls {
        if !record.tls_enabled {
            return Err("Vault NATS TLS endpoints require tls_enabled=true".to_owned());
        }
        let trust_source: TlsTrustSource = record
            .tls_trust_source
            .as_deref()
            .ok_or("Vault NATS tls_trust_source is required")?
            .parse()?;
        let ca_cert = match trust_source {
            TlsTrustSource::System => {
                if record.ca_cert_path.is_some() {
                    return Err("Vault NATS system trust must not include ca_cert_path".to_owned());
                }
                None
            }
            TlsTrustSource::File => Some(
                record
                    .ca_cert_path
                    .ok_or("Vault NATS file trust requires ca_cert_path")?
                    .into(),
            ),
        };
        let (client_cert, client_key) = match (record.client_cert_path, record.client_key_path) {
            (Some(cert), Some(key)) => (Some(cert.into()), Some(key.into())),
            (None, None) => (None, None),
            _ => {
                return Err(
                    "Vault NATS client certificate and key must be configured together".to_owned(),
                )
            }
        };
        Some(TlsClientConfig {
            trust_source,
            ca_cert,
            client_cert,
            client_key,
        })
    } else {
        if record.tls_enabled || has_tls_material {
            return Err(
                "Vault NATS TLS material is not allowed for plaintext endpoints".to_owned(),
            );
        }
        None
    };
    let client_name = record.client_name.trim().to_owned();
    if client_name.is_empty() {
        return Err("Vault NATS client_name is required".to_owned());
    }

    config.urls = urls;
    config.client_name = client_name;
    config.auth_mode = auth_mode;
    config.token = token;
    config.username = username;
    config.password = password;
    config.credentials_file = credentials_file;
    config.tls = tls;
    config.tls_first = record.tls_first;
    Ok(())
}

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

fn validate_auth(
    mode: NatsAuthMode,
    token: &Option<String>,
    username: &Option<String>,
    password: &Option<String>,
    credentials_file: &Option<PathBuf>,
) -> Result<(), String> {
    let configured_methods = usize::from(token.is_some())
        + usize::from(username.is_some() || password.is_some())
        + usize::from(credentials_file.is_some());
    if configured_methods > 1 {
        return Err("Vault NATS authentication methods are mutually exclusive".to_owned());
    }
    match mode {
        NatsAuthMode::None if configured_methods == 0 => Ok(()),
        NatsAuthMode::Token if token.is_some() => Ok(()),
        NatsAuthMode::UserPassword if username.is_some() && password.is_some() => Ok(()),
        NatsAuthMode::CredentialsFile if credentials_file.is_some() => Ok(()),
        NatsAuthMode::None => {
            Err("Vault NATS auth_mode=none cannot include credentials".to_owned())
        }
        NatsAuthMode::Token => Err("Vault NATS token auth requires token".to_owned()),
        NatsAuthMode::UserPassword => {
            Err("Vault NATS user_password auth requires username and password".to_owned())
        }
        NatsAuthMode::CredentialsFile => {
            Err("Vault NATS credentials_file auth requires credentials_file".to_owned())
        }
    }
}
