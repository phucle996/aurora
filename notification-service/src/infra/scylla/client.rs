use crate::application::ports::AppError;
use crate::config::{ScyllaConfig, ScyllaTlsMode};
use crate::infra::vault::VaultClient;
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use scylla::client::execution_profile::ExecutionProfile;
use scylla::client::session::Session;
use scylla::client::session_builder::SessionBuilder;
use scylla::statement::Consistency;
use std::fs::File;
use std::io::BufReader;
use std::path::Path;
use std::sync::Arc;

const CONNECTION_PATH: &str = "secret/data/connections/scylla/central/role-notification-service";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    contact_points: Vec<String>,
    local_dc: String,
    keyspace: String,
    username: String,
    password: String,
    tls_mode: String,
    ca_cert_path: Option<String>,
    client_cert_path: Option<String>,
    client_key_path: Option<String>,
}

pub async fn resolve_from_vault(
    vault: &VaultClient,
    config: &mut ScyllaConfig,
) -> Result<(), AppError> {
    let record: ConnectionRecord = vault
        .read(CONNECTION_PATH)
        .await
        .map_err(|error| Box::new(std::io::Error::other(error)) as AppError)?;
    if record.schema_version != 1 {
        return Err(invalid(format!(
            "unsupported Vault Scylla schema_version {}",
            record.schema_version
        )));
    }
    let contact_points = record
        .contact_points
        .iter()
        .map(|value| value.trim())
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if contact_points.is_empty() {
        return Err(invalid("Vault Scylla contact_points is required"));
    }
    if !valid_identifier(&record.local_dc) {
        return Err(invalid("Vault Scylla local_dc is invalid"));
    }
    if !valid_identifier(&record.keyspace) {
        return Err(invalid("Vault Scylla keyspace is invalid"));
    }
    if record.username.trim().is_empty() || record.password.trim().is_empty() {
        return Err(invalid("Vault Scylla credentials are required"));
    }
    let tls_mode = match record.tls_mode.trim().to_ascii_lowercase().as_str() {
        "disabled" => {
            if record.ca_cert_path.is_some()
                || record.client_cert_path.is_some()
                || record.client_key_path.is_some()
            {
                return Err(invalid(
                    "Vault Scylla TLS material is not allowed when tls_mode=disabled",
                ));
            }
            ScyllaTlsMode::Disabled
        }
        "server" => {
            if record.client_cert_path.is_some() || record.client_key_path.is_some() {
                return Err(invalid(
                    "Vault Scylla server TLS must not include client certificate material",
                ));
            }
            if record.ca_cert_path.is_none() {
                return Err(invalid("Vault Scylla server TLS requires ca_cert_path"));
            }
            ScyllaTlsMode::Server
        }
        "mutual" => {
            if record.ca_cert_path.is_none()
                || record.client_cert_path.is_none()
                || record.client_key_path.is_none()
            {
                return Err(invalid(
                    "Vault Scylla mutual TLS requires CA, client certificate and key",
                ));
            }
            ScyllaTlsMode::Mutual
        }
        _ => return Err(invalid("Vault Scylla tls_mode is invalid")),
    };

    config.contact_points = contact_points;
    config.local_dc = record.local_dc;
    config.keyspace = record.keyspace;
    config.username = record.username;
    config.password = record.password;
    config.tls = crate::config::ScyllaTlsConfig {
        mode: tls_mode,
        ca_cert: record.ca_cert_path.map(Into::into),
        client_cert: record.client_cert_path.map(Into::into),
        client_key: record.client_key_path.map(Into::into),
    };
    Ok(())
}

pub async fn connect(config: &ScyllaConfig) -> Result<Arc<Session>, AppError> {
    let profile = ExecutionProfile::builder()
        .consistency(Consistency::LocalQuorum)
        .request_timeout(Some(config.request_timeout))
        .build();
    let mut builder = config
        .contact_points
        .iter()
        .fold(SessionBuilder::new(), |builder, node| {
            builder.known_node(node)
        })
        .user(&config.username, &config.password)
        .prefer_datacenter(config.local_dc.clone())
        .connection_timeout(config.connect_timeout)
        .default_execution_profile_handle(profile.into_handle());

    if config.tls.mode != ScyllaTlsMode::Disabled {
        builder = builder.tls_context(Some(Arc::new(build_tls(config)?)));
    }

    let session = Arc::new(builder.build().await?);
    if config.auto_schema {
        super::schema::ensure(&session, config).await?;
    }
    session.use_keyspace(&config.keyspace, false).await?;
    super::schema::verify(&session).await?;
    Ok(session)
}

fn build_tls(config: &ScyllaConfig) -> Result<rustls::ClientConfig, AppError> {
    let ca_path = config
        .tls
        .ca_cert
        .as_deref()
        .ok_or_else(|| invalid("Scylla TLS CA path is missing"))?;
    let mut roots = rustls::RootCertStore::empty();
    for certificate in read_certificates(ca_path)? {
        roots.add(certificate)?;
    }
    if roots.is_empty() {
        return Err(invalid("Scylla TLS root store is empty"));
    }

    let builder = rustls::ClientConfig::builder().with_root_certificates(roots);
    match config.tls.mode {
        ScyllaTlsMode::Disabled => Err(invalid("Scylla TLS builder called in disabled mode")),
        ScyllaTlsMode::Server => Ok(builder.with_no_client_auth()),
        ScyllaTlsMode::Mutual => {
            let cert_path = config
                .tls
                .client_cert
                .as_deref()
                .ok_or_else(|| invalid("Scylla mTLS client certificate path is missing"))?;
            let key_path = config
                .tls
                .client_key
                .as_deref()
                .ok_or_else(|| invalid("Scylla mTLS client key path is missing"))?;
            let certificates = read_certificates(cert_path)?;
            let private_key = read_private_key(key_path)?;
            Ok(builder.with_client_auth_cert(certificates, private_key)?)
        }
    }
}

fn read_certificates(path: &Path) -> Result<Vec<CertificateDer<'static>>, AppError> {
    let mut reader = BufReader::new(File::open(path)?);
    let certificates = rustls_pemfile::certs(&mut reader).collect::<Result<Vec<_>, _>>()?;
    if certificates.is_empty() {
        return Err(invalid("certificate file contains no certificates"));
    }
    Ok(certificates)
}

fn read_private_key(path: &Path) -> Result<PrivateKeyDer<'static>, AppError> {
    let mut reader = BufReader::new(File::open(path)?);
    rustls_pemfile::private_key(&mut reader)?
        .ok_or_else(|| invalid("private key file contains no supported key"))
}

fn invalid(message: impl Into<String>) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.into()).into()
}

fn valid_identifier(value: &str) -> bool {
    value.len() <= 48
        && value.as_bytes().first().is_some_and(u8::is_ascii_lowercase)
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}
