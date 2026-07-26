use tonic::transport::{Certificate, Identity, Server, ServerTlsConfig};

use envoy_types::pb::envoy::service::auth::v3::authorization_server::AuthorizationServer;

use crate::access_store::AccessStore;
use crate::assertion::AssertionVerifier;
use crate::check::StorageAuthzService;
use crate::config::Config;
use crate::error::AuthzError;
use crate::keys::AssertionKeys;

pub async fn run(config: Config) -> Result<(), AuthzError> {
    let access = AccessStore::connect(&config).await?;
    let keys = AssertionKeys::new(config.public_keys.clone())?;
    let verifier =
        AssertionVerifier::new(config.zone_id.clone(), keys, config.replay_cache_capacity);
    let service = StorageAuthzService::new(verifier, access);

    let cert = tokio::fs::read(&config.server_cert)
        .await
        .map_err(|error| {
            AuthzError::Configuration(format!("read server certificate failed: {error}"))
        })?;
    let key = tokio::fs::read(&config.server_key)
        .await
        .map_err(|error| AuthzError::Configuration(format!("read server key failed: {error}")))?;
    let client_ca = tokio::fs::read(&config.client_ca)
        .await
        .map_err(|error| AuthzError::Configuration(format!("read client CA failed: {error}")))?;
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(cert, key))
        .client_ca_root(Certificate::from_pem(client_ca));

    tracing::info!(event_code = "ZONE_STORAGE_AUTHZ_STARTED", address = %config.listen_addr, zone_id = %config.zone_id);
    let shutdown = async {
        let ctrl_c = tokio::signal::ctrl_c();
        #[cfg(unix)]
        {
            let mut sigterm =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                    .expect("install SIGTERM handler");
            tokio::select! {
                _ = ctrl_c => {}
                _ = sigterm.recv() => {}
            }
        }
        #[cfg(not(unix))]
        let _ = ctrl_c.await;
        tracing::info!(event_code = "ZONE_STORAGE_AUTHZ_SHUTDOWN");
    };

    Server::builder()
        .tls_config(tls)
        .map_err(|error| AuthzError::Configuration(format!("configure gRPC mTLS failed: {error}")))?
        .add_service(AuthorizationServer::new(service))
        .serve_with_shutdown(config.listen_addr, shutdown)
        .await
        .map_err(|error| AuthzError::Dependency(format!("gRPC server failed: {error}")))
}
