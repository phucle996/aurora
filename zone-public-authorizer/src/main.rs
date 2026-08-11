use std::{
    env,
    path::PathBuf,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_nats::jetstream::{self, stream::StorageType};
use bytes::Bytes;
use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::{Authorization, AuthorizationServer},
    CheckRequest, CheckResponse,
};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tokio::sync::Semaphore;
use tonic::{Request, Response, Status};
use zone_transfer_contract::{
    TransferTicketState, TransferTicketV1, TRANSFER_TICKET_SCHEMA_VERSION,
};

#[derive(Clone)]
struct TicketStore {
    store: jetstream::kv::Store,
    timeout: Duration,
}

#[derive(Clone)]
struct PublicAuthorizer {
    store: TicketStore,
    zone_id: String,
    inflight: Arc<Semaphore>,
}

#[tonic::async_trait]
impl Authorization for PublicAuthorizer {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        let Ok(_permit) = self.inflight.clone().try_acquire_owned() else {
            return Err(Status::resource_exhausted(
                "Zone public authorizer overloaded",
            ));
        };
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
        let token = headers
            .get("x-aurora-transfer-ticket")
            .ok_or_else(|| Status::permission_denied("Transfer ticket missing"))?;
        let (ticket_id, secret) = token
            .split_once('.')
            .ok_or_else(|| Status::permission_denied("Transfer ticket invalid"))?;
        if ticket_id.is_empty() || secret.len() < 32 {
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Transfer ticket invalid"),
            )));
        }
        let entry = tokio::time::timeout(
            self.store.timeout,
            self.store.store.entry(ticket_id.to_string()),
        )
        .await
        .map_err(|_| Status::unavailable("Transfer ticket store unavailable"))?
        .map_err(|_| Status::unavailable("Transfer ticket store unavailable"))?;
        let Some(entry) = entry else {
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Transfer ticket invalid"),
            )));
        };
        let mut ticket: TransferTicketV1 = serde_json::from_slice(&entry.value)
            .map_err(|_| Status::unavailable("Transfer ticket store corrupt"))?;
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| Status::unavailable("System clock unavailable"))?
            .as_secs();
        let actual_hash = format!("{:x}", Sha256::digest(secret.as_bytes()));
        let length = headers
            .get("content-length")
            .and_then(|value| value.parse::<u64>().ok());
        let content_type = headers.get("content-type");
        if ticket.schema_version != TRANSFER_TICKET_SCHEMA_VERSION
            || ticket.zone_id != self.zone_id
            || ticket.state != TransferTicketState::Issued
            || ticket.expires_at_unix_seconds <= now
            || ticket
                .secret_sha256
                .as_bytes()
                .ct_eq(actual_hash.as_bytes())
                .unwrap_u8()
                != 1
            || ticket.method != http.method
            || ticket.public_path != http.path
            || !matches!(http.method.as_str(), "GET" | "PUT")
            || ticket.content_length != length
            || ticket.content_type.as_deref() != content_type.map(String::as_str)
        {
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Transfer ticket denied"),
            )));
        }
        ticket.state = TransferTicketState::Consuming;
        let value = Bytes::from(
            serde_json::to_vec(&ticket)
                .map_err(|_| Status::unavailable("Transfer ticket encode failed"))?,
        );
        match tokio::time::timeout(
            self.store.timeout,
            self.store.store.update(ticket_id, value, entry.revision),
        )
        .await
        {
            Err(_) => return Err(Status::unavailable("Transfer ticket consume unavailable")),
            Ok(Err(_)) => {
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Transfer ticket already consumed"),
                )))
            }
            Ok(Ok(_)) => {}
        }
        let mut response = CheckResponse::with_status(Status::ok("authorized"));
        response.set_http_response(
            envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
        );
        if let Some(
            envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse::OkResponse(ok),
        ) = response.http_response.as_mut()
        {
            use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
            for (key, value) in [
                ("x-aurora-actor-id", ticket.actor_id),
                ("x-aurora-resource-id", ticket.resource_id),
                ("x-aurora-operation-id", ticket.operation_id),
                ("x-aurora-transfer-capability", ticket.capability),
            ] {
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: key.to_string(),
                        value,
                        ..Default::default()
                    }),
                    append_action: 2,
                    ..Default::default()
                });
            }
            ok.headers_to_remove
                .push("x-aurora-transfer-ticket".to_string());
        }
        Ok(Response::new(response))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let zone_id = required("ZONE_ID")?;
    if uuid::Uuid::parse_str(&zone_id).is_err() {
        return Err("ZONE_ID must be a UUID".into());
    }
    let listen = required("ZONE_PUBLIC_AUTHORIZER_LISTEN")?.parse()?;
    let timeout = Duration::from_millis(parsed("NATS_ZONE_REQUEST_TIMEOUT_MS", 2_000_u64)?);
    let replicas = parsed("ZONE_TRANSFER_KV_REQUIRED_REPLICAS", 3_usize)?;
    let options = async_nats::ConnectOptions::new()
        .add_root_certificates(PathBuf::from(required("NATS_ZONE_TLS_CA")?))
        .require_tls(true)
        .add_client_certificate(
            PathBuf::from(required("NATS_ZONE_TLS_CERT")?),
            PathBuf::from(required("NATS_ZONE_TLS_KEY")?),
        )
        .credentials_file(PathBuf::from(required("NATS_ZONE_CREDS")?))
        .await?;
    let client =
        tokio::time::timeout(timeout, options.connect(required("NATS_ZONE_URL")?)).await??;
    let store = jetstream::new(client)
        .get_key_value("AURORA_ZONE_TRANSFER")
        .await?;
    let status = store.status().await?;
    if status.history() != 1
        || status.info.config.storage != StorageType::File
        || status.info.config.num_replicas < replicas
    {
        return Err("Zone transfer KV durability contract mismatch".into());
    }
    let service = PublicAuthorizer {
        store: TicketStore { store, timeout },
        zone_id,
        inflight: Arc::new(Semaphore::new(parsed(
            "ZONE_PUBLIC_AUTHORIZER_MAX_INFLIGHT",
            1024_usize,
        )?)),
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
