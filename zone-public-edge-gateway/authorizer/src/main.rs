use std::{env, path::PathBuf, time::Duration};

use async_nats::jetstream::{self, stream::StorageType};
use envoy_types::ext_authz::v3::CheckRequestExt;
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::{Authorization, AuthorizationServer},
    CheckRequest, CheckResponse,
};
use tonic::{Request, Response, Status};

mod runtime_read;
mod storage_access;

mod transfer_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.zone.transfer.v1.rs"));
}

#[derive(Clone)]
struct PublicAuthorizer {
    storage_access: storage_access::StorageAccessAuthorizer,
    runtime_read: runtime_read::RuntimeReadAuthorizer,
}

#[tonic::async_trait]
impl Authorization for PublicAuthorizer {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        let headers = request
            .get_ref()
            .get_client_headers()
            .ok_or_else(|| Status::permission_denied("HTTP context missing"))?;
        let http = request
            .get_ref()
            .attributes
            .as_ref()
            .and_then(|value| value.request.as_ref())
            .and_then(|value| value.http.as_ref())
            .ok_or_else(|| Status::permission_denied("HTTP context missing"))?;
        let response = if http
            .path
            .split('?')
            .next()
            .is_some_and(|path| path.starts_with("/zone-public/v1/runtime/"))
        {
            self.runtime_read
                .authorize(headers, &http.method, &http.path)
                .await?
        } else {
            self.storage_access
                .authorize(headers, &http.method, &http.path)
                .await?
        };
        Ok(Response::new(response))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let _ = rustls::crypto::ring::default_provider().install_default();

    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let zone_id = required("ZONE_ID")?;
    if uuid::Uuid::parse_str(&zone_id).is_err() {
        return Err("ZONE_ID must be a UUID".into());
    }
    let listen = required("ZONE_PUBLIC_AUTHORIZER_LISTEN")?.parse()?;
    let timeout_ms = parsed("NATS_ZONE_REQUEST_TIMEOUT_MS", 350_u64)?;
    if !(1..=450).contains(&timeout_ms) {
        return Err("NATS_ZONE_REQUEST_TIMEOUT_MS must be between 1 and 450".into());
    }
    let timeout = Duration::from_millis(timeout_ms);
    let connect_timeout_ms = parsed("NATS_ZONE_CONNECT_TIMEOUT_MS", 5_000_u64)?;
    if !(100..=30_000).contains(&connect_timeout_ms) {
        return Err("NATS_ZONE_CONNECT_TIMEOUT_MS must be between 100 and 30000".into());
    }
    let replicas = parsed("ZONE_PUBLIC_KV_REQUIRED_REPLICAS", 3_usize)?;
    let storage_max_inflight = parsed("ZONE_STORAGE_AUTHORIZER_MAX_INFLIGHT", 768_usize)?;
    let runtime_max_inflight = parsed("ZONE_RUNTIME_AUTHORIZER_MAX_INFLIGHT", 256_usize)?;
    if !(1..=100_000).contains(&storage_max_inflight)
        || !(1..=100_000).contains(&runtime_max_inflight)
    {
        return Err("Zone authorizer workflow concurrency budget is invalid".into());
    }
    let options = async_nats::ConnectOptions::new()
        .add_root_certificates(PathBuf::from(required("NATS_ZONE_TLS_CA")?))
        .require_tls(true)
        .add_client_certificate(
            PathBuf::from(required("NATS_ZONE_TLS_CERT")?),
            PathBuf::from(required("NATS_ZONE_TLS_KEY")?),
        )
        .credentials_file(PathBuf::from(required("NATS_ZONE_CREDS")?))
        .await?;
    let connect_timeout = Duration::from_millis(connect_timeout_ms);
    let client = tokio::time::timeout(connect_timeout, options.connect(required("NATS_ZONE_URL")?))
        .await??;
    let transfer_store = tokio::time::timeout(
        connect_timeout,
        jetstream::new(client.clone()).get_key_value("AURORA_ZONE_TRANSFER"),
    )
    .await??;
    let runtime_store = tokio::time::timeout(
        connect_timeout,
        jetstream::new(client.clone()).get_key_value("AURORA_ZONE_CONFIG"),
    )
    .await??;
    let runtime_replay_store = tokio::time::timeout(
        connect_timeout,
        jetstream::new(client.clone()).get_key_value("AURORA_ZONE_RUNTIME_REPLAY"),
    )
    .await??;
    let admission_store = tokio::time::timeout(
        connect_timeout,
        jetstream::new(client).get_key_value("AURORA_ZONE_ADMISSION"),
    )
    .await??;
    let transfer_status = tokio::time::timeout(connect_timeout, transfer_store.status()).await??;
    if transfer_status.history() != 1
        || transfer_status.info.config.storage != StorageType::File
        || transfer_status.info.config.num_replicas < replicas
    {
        return Err("Zone transfer KV durability contract mismatch".into());
    }
    let admission_status =
        tokio::time::timeout(connect_timeout, admission_store.status()).await??;
    if admission_status.history() != 1
        || admission_status.info.config.storage != StorageType::File
        || admission_status.info.config.num_replicas < replicas
    {
        return Err("Zone admission KV durability contract mismatch".into());
    }
    let runtime_status = tokio::time::timeout(connect_timeout, runtime_store.status()).await??;
    if runtime_status.history() != 1
        || runtime_status.info.config.storage != StorageType::File
        || runtime_status.info.config.num_replicas < replicas
    {
        return Err("Zone runtime registry KV durability contract mismatch".into());
    }
    let runtime_replay_status =
        tokio::time::timeout(connect_timeout, runtime_replay_store.status()).await??;
    if runtime_replay_status.history() != 1
        || runtime_replay_status.info.config.storage != StorageType::File
        || runtime_replay_status.info.config.num_replicas < replicas
        || runtime_replay_status.info.config.max_age != Duration::from_secs(30)
    {
        return Err("Zone runtime replay KV durability contract mismatch".into());
    }
    let runtime_read = runtime_read::RuntimeReadAuthorizer::new(
        runtime_store,
        runtime_replay_store,
        zone_id.clone(),
        &required("ZONE_RUNTIME_ASSERTION_PUBLIC_KEYS")?,
        timeout,
        runtime_max_inflight,
    )?;
    let service = PublicAuthorizer {
        storage_access: storage_access::StorageAccessAuthorizer::new(
            transfer_store,
            admission_store,
            timeout,
            zone_id,
            storage_max_inflight,
        ),
        runtime_read,
    };
    tracing::info!(event_code = "ZONE_PUBLIC_AUTHORIZER_STARTED", address = %listen);
    tonic::transport::Server::builder()
        .add_service(AuthorizationServer::new(service))
        .serve(listen)
        .await?;
    Ok(())
}

fn required(name: &str) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
    Ok(env::var(name)?)
}

fn parsed<T: std::str::FromStr>(
    name: &str,
    default: T,
) -> Result<T, Box<dyn std::error::Error + Send + Sync>> {
    Ok(match env::var(name) {
        Ok(value) => value.parse().map_err(|_| format!("{name} is invalid"))?,
        Err(_) => default,
    })
}
