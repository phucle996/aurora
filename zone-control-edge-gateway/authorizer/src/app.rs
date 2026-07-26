use std::sync::Arc;

use tonic::transport::{Certificate, Identity, Server, ServerTlsConfig};

use envoy_types::pb::envoy::service::auth::v3::authorization_server::AuthorizationServer;
use tokio::sync::watch;

use crate::authorization::ZoneControlAuthorizer;
use crate::config::Config;
use crate::control_assertion::AssertionVerifier;
use crate::error::AuthzError;
use crate::keys::AssertionKeys;
use crate::telemetry::{self, Telemetry};
use crate::zone_access::AccessStore;

pub async fn run(config: Config) -> Result<(), AuthzError> {
    let telemetry = Arc::new(Telemetry::default());
    let access = AccessStore::connect(&config, telemetry.clone()).await?;
    let keys = AssertionKeys::new(config.public_keys.clone())?;
    let verifier =
        AssertionVerifier::new(config.zone_id.clone(), keys, config.replay_cache_capacity);
    let service = ZoneControlAuthorizer::new(
        verifier,
        access.clone(),
        telemetry.clone(),
        config.max_inflight_checks,
    );

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

    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    let access_watch = access.start_watch(shutdown_rx.clone());
    let telemetry_listener = telemetry::bind(config.telemetry_addr).await?;
    let telemetry_shutdown = shutdown_tx.clone();
    let telemetry_shutdown_rx = shutdown_rx.clone();
    let telemetry_state = telemetry.clone();
    let telemetry_server = tokio::spawn(async move {
        let result =
            telemetry::serve(telemetry_listener, telemetry_state, telemetry_shutdown_rx).await;
        if result.is_err() {
            // Health/metrics failure removes the gRPC server from service
            // instead of leaving an unobservable ready-looking process.
            let _ = telemetry_shutdown.send(true);
        }
        result
    });
    let signal_tx = shutdown_tx.clone();
    let signal = tokio::spawn(async move {
        shutdown_signal().await;
        let _ = signal_tx.send(true);
    });

    tracing::info!(
        event_code = "ZONE_CONTROL_AUTHORIZER_STARTED",
        address = %config.listen_addr,
        telemetry_address = %config.telemetry_addr,
        zone_id = %config.zone_id
    );
    let server_result = Server::builder()
        .tls_config(tls)
        .map_err(|error| AuthzError::Configuration(format!("configure gRPC mTLS failed: {error}")))?
        .add_service(AuthorizationServer::new(service))
        .serve_with_shutdown(config.listen_addr, wait_for_shutdown(shutdown_rx))
        .await
        .map_err(|error| AuthzError::Dependency(format!("gRPC server failed: {error}")));

    telemetry.set_ready(false);
    let _ = shutdown_tx.send(true);
    signal.abort();
    let _ = access_watch.await;
    match telemetry_server.await {
        Ok(Ok(())) => {}
        Ok(Err(error)) if server_result.is_ok() => return Err(error),
        Ok(Err(_)) | Err(_) => {}
    }
    tracing::info!(event_code = "ZONE_CONTROL_AUTHORIZER_SHUTDOWN");
    server_result
}

async fn wait_for_shutdown(mut shutdown: watch::Receiver<bool>) {
    while !*shutdown.borrow() {
        if shutdown.changed().await.is_err() {
            return;
        }
    }
}

async fn shutdown_signal() {
    let ctrl_c = tokio::signal::ctrl_c();
    #[cfg(unix)]
    {
        let mut sigterm = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("install SIGTERM handler");
        tokio::select! {
            _ = ctrl_c => {}
            _ = sigterm.recv() => {}
        }
    }
    #[cfg(not(unix))]
    let _ = ctrl_c.await;
}
