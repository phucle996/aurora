use crate::application::ports::AppError;
use crate::config::{ScyllaConfig, ScyllaTlsMode};
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use scylla::client::execution_profile::ExecutionProfile;
use scylla::client::session::Session;
use scylla::client::session_builder::SessionBuilder;
use scylla::statement::Consistency;
use std::fs::File;
use std::io::BufReader;
use std::path::Path;
use std::sync::Arc;

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

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.to_owned()).into()
}
