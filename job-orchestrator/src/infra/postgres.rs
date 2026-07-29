use crate::config::{PostgresConfig, PostgresTlsMode, TlsClientConfig, TlsTrustSource};
use crate::infra::vault::VaultClient;
use crate::observability::logger::Logger;
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use rustls::{ClientConfig, RootCertStore};
use std::fs::File;
use std::io::{self, BufReader};
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::config::SslMode;
use tokio_postgres::Client;
use tokio_postgres_rustls::MakeRustlsConnect;

const CONNECTION_PATH: &str = "secret/data/connections/postgres/pg-central/role-cdc-read";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    database_url: String,
}

pub async fn resolve_from_vault(
    vault: &VaultClient,
    config: &mut PostgresConfig,
) -> Result<(), String> {
    let record: ConnectionRecord = vault.read(CONNECTION_PATH).await?;
    if record.schema_version != 1 {
        return Err(format!(
            "unsupported Vault PostgreSQL schema_version {}",
            record.schema_version
        ));
    }
    record
        .database_url
        .parse::<tokio_postgres::Config>()
        .map_err(|error| format!("Vault database_url is invalid: {error}"))?;
    config.database_url = record.database_url;
    Ok(())
}

/// Opens a SQL connection using the same TLS identity as logical replication.
/// Connection ownership remains local to the worker; the protocol driver is
/// supervised so a socket failure is visible without leaking a detached error.
pub async fn connect(config: &PostgresConfig, owner: &'static str) -> io::Result<Client> {
    let mut postgres = config
        .database_url
        .parse::<tokio_postgres::Config>()
        .map_err(invalid_config)?;
    postgres
        .application_name(&config.application_name)
        .connect_timeout(Duration::from_secs(config.connect_timeout_secs))
        .tcp_user_timeout(Duration::from_millis(config.tcp_user_timeout_ms))
        .keepalives(true)
        .keepalives_idle(Duration::from_secs(config.keepalive_idle_secs))
        .keepalives_interval(Duration::from_secs(config.keepalive_interval_secs))
        .keepalives_retries(config.keepalive_retries);

    let client = match config.tls_mode {
        PostgresTlsMode::Disable => {
            postgres.ssl_mode(SslMode::Disable);
            let (client, connection) = postgres
                .connect(tokio_postgres::NoTls)
                .await
                .map_err(io::Error::other)?;
            supervise(owner, connection);
            client
        }
        PostgresTlsMode::VerifyFull => {
            postgres.ssl_mode(SslMode::Require);
            let tls_config = config
                .tls
                .as_ref()
                .ok_or_else(|| invalid_config("PostgreSQL TLS identity is missing"))?;
            let tls = MakeRustlsConnect::new(build_rustls_config(tls_config)?);
            let (client, connection) = postgres.connect(tls).await.map_err(io::Error::other)?;
            supervise(owner, connection);
            client
        }
    };

    // Apply a bounded session budget to every caller, including code paths that
    // previously forgot to configure transaction/lock timeouts.
    client
        .batch_execute(&format!(
            "SET statement_timeout = {}; \
             SET lock_timeout = {}; \
             SET idle_in_transaction_session_timeout = {}",
            config.statement_timeout_ms, config.lock_timeout_ms, config.idle_transaction_timeout_ms,
        ))
        .await
        .map_err(io::Error::other)?;
    Ok(client)
}

fn supervise<S, T>(owner: &'static str, connection: tokio_postgres::Connection<S, T>)
where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
    T: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
{
    tokio::spawn(async move {
        if let Err(error) = connection.await {
            Logger::sys_error(
                owner,
                "PostgreSQL connection driver stopped",
                &error.to_string(),
            );
        }
    });
}

fn build_rustls_config(tls: &TlsClientConfig) -> io::Result<ClientConfig> {
    let mut roots = RootCertStore::empty();
    match tls.trust_source {
        TlsTrustSource::System => {
            roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
        }
        TlsTrustSource::File => {
            let ca_path = tls
                .ca_cert
                .as_ref()
                .ok_or_else(|| invalid_config("PostgreSQL file trust requires a CA path"))?;
            for certificate in load_certificates(ca_path)? {
                roots.add(certificate).map_err(invalid_config)?;
            }
        }
    }
    if roots.is_empty() {
        return Err(invalid_config("PostgreSQL TLS root store is empty"));
    }

    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let builder = ClientConfig::builder_with_provider(provider)
        .with_safe_default_protocol_versions()
        .map_err(invalid_config)?
        .with_root_certificates(roots);

    match (&tls.client_cert, &tls.client_key) {
        (Some(cert_path), Some(key_path)) => builder
            .with_client_auth_cert(load_certificates(cert_path)?, load_private_key(key_path)?)
            .map_err(invalid_config),
        (None, None) => Ok(builder.with_no_client_auth()),
        _ => Err(invalid_config(
            "PostgreSQL client certificate and key must be configured together",
        )),
    }
}

fn load_certificates(path: &std::path::Path) -> io::Result<Vec<CertificateDer<'static>>> {
    let mut reader = BufReader::new(File::open(path)?);
    let certificates = rustls_pemfile::certs(&mut reader)
        .collect::<Result<Vec<_>, _>>()
        .map_err(invalid_config)?;
    if certificates.is_empty() {
        return Err(invalid_config(format!(
            "no PEM certificate found in {}",
            path.display()
        )));
    }
    Ok(certificates)
}

fn load_private_key(path: &std::path::Path) -> io::Result<PrivateKeyDer<'static>> {
    let mut reader = BufReader::new(File::open(path)?);
    rustls_pemfile::private_key(&mut reader)
        .map_err(invalid_config)?
        .ok_or_else(|| invalid_config(format!("no PEM private key found in {}", path.display())))
}

fn invalid_config(error: impl std::fmt::Display) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, error.to_string())
}
