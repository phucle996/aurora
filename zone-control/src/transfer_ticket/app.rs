use std::{
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use axum::{
    extract::{Path, State},
    http::{header, HeaderMap, StatusCode},
    response::IntoResponse,
    routing::{delete, get, post},
    Json, Router,
};
use base64::Engine;
use prost::Message;
use rand::RngCore;
use serde::Serialize;
use sha2::{Digest, Sha256};

use super::{
    config::Config,
    store::TicketStore,
    transfer_proto::{TransferGrantV1, TransferTicketState, TransferTicketV1},
};
use crate::transfer_ticket::TRANSFER_TICKET_SCHEMA_VERSION;

const GRANT_HEADER: &str = "x-aurora-transfer-grant";

#[derive(Clone)]
struct AppState {
    config: Config,
    store: TicketStore,
}

#[derive(Serialize)]
struct IssueResponse {
    method: String,
    url: String,
    transfer_ticket: String,
    expires_at_unix_seconds: u64,
}

pub async fn run(
    config: Config,
    store: TicketStore,
    _shutdown: tokio_util::sync::CancellationToken,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let state = Arc::new(AppState { config, store });
    let app = Router::new()
        .route("/healthz", get(|| async { StatusCode::NO_CONTENT }))
        .route("/zone-control/v1/transfer-tickets", post(issue))
        .route(
            "/zone-control/v1/transfer-tickets/:ticket_id",
            delete(revoke),
        )
        .with_state(state.clone());
    let listener = tokio::net::TcpListener::bind(state.config.listen_addr).await?;
    tracing::info!(event_code = "ZONE_CONTROL_STARTED", address = %state.config.listen_addr, zone_id = %state.config.zone_id, workflows = "orchestrator,transfer_ticket");
    axum::serve(listener, app).await?;
    Ok(())
}

async fn issue(State(state): State<Arc<AppState>>, headers: HeaderMap) -> impl IntoResponse {
    let grant = match decode_grant(&headers, &state.config.zone_id) {
        Ok(grant) => grant,
        Err(code) => return code.into_response(),
    };
    let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
        Ok(value) => value.as_secs(),
        Err(_) => return StatusCode::SERVICE_UNAVAILABLE.into_response(),
    };
    let expires_at_unix_seconds = now.saturating_add(state.config.ticket_ttl.as_secs());
    for _ in 0..3 {
        let ticket_id = uuid::Uuid::new_v4().to_string();
        let mut secret_bytes = [0_u8; 32];
        rand::rng().fill_bytes(&mut secret_bytes);
        let secret = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(secret_bytes);
        let ticket = TransferTicketV1 {
            schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
            ticket_id: ticket_id.clone(),
            secret_sha256: format!("{:x}", Sha256::digest(secret.as_bytes())),
            capability: grant.capability.clone(),
            actor_id: grant.actor_id.clone(),
            zone_id: grant.zone_id.clone(),
            resource_id: grant.resource_id.clone(),
            workspace_id: grant.workspace_id.clone(),
            operation_id: grant.operation_id.clone(),
            method: grant.method.clone(),
            public_path: grant.public_path.clone(),
            content_length: grant.content_length,
            content_type: grant.content_type.clone(),
            issued_at_unix_seconds: now,
            expires_at_unix_seconds,
            one_time: grant.one_time,
            state: TransferTicketState::Issued as i32,
        };
        match state.store.create(&ticket).await {
            Ok(()) => {
                return (
                    [
                        (header::CACHE_CONTROL, "no-store"),
                        (header::CONTENT_TYPE, "application/json"),
                    ],
                    Json(IssueResponse {
                        method: grant.method,
                        url: format!("{}{}", state.config.public_base_url, grant.public_path),
                        transfer_ticket: format!("{ticket_id}.{secret}"),
                        expires_at_unix_seconds,
                    }),
                )
                    .into_response()
            }
            Err(error) if error.contains("wrong last sequence") => continue,
            Err(error) => {
                tracing::error!(event_code = "ZONE_TRANSFER_TICKET_CREATE_FAILED", error = %error);
                return StatusCode::SERVICE_UNAVAILABLE.into_response();
            }
        }
    }
    StatusCode::SERVICE_UNAVAILABLE.into_response()
}

async fn revoke(
    Path(ticket_id): Path<String>,
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
) -> impl IntoResponse {
    if uuid::Uuid::parse_str(&ticket_id).is_err() {
        return StatusCode::NOT_FOUND;
    }
    let grant = match decode_grant(&headers, &state.config.zone_id) {
        Ok(grant) => grant,
        Err(code) => return code,
    };
    match state.store.revoke(&ticket_id, &grant).await {
        Ok(true) => StatusCode::NO_CONTENT,
        Ok(false) => StatusCode::NOT_FOUND,
        Err(error) => {
            tracing::error!(event_code = "ZONE_TRANSFER_TICKET_REVOKE_FAILED", error = %error);
            StatusCode::SERVICE_UNAVAILABLE
        }
    }
}

fn decode_grant(headers: &HeaderMap, expected_zone: &str) -> Result<TransferGrantV1, StatusCode> {
    let encoded = headers
        .get(GRANT_HEADER)
        .and_then(|value| value.to_str().ok())
        .ok_or(StatusCode::FORBIDDEN)?;
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(encoded)
        .map_err(|_| StatusCode::FORBIDDEN)?;
    let grant = TransferGrantV1::decode(bytes.as_slice()).map_err(|_| StatusCode::FORBIDDEN)?;
    if grant.schema_version != TRANSFER_TICKET_SCHEMA_VERSION
        || grant.zone_id != expected_zone
        || !matches!(grant.method.as_str(), "PUT" | "GET" | "POST" | "DELETE")
        || !grant.public_path.starts_with('/')
        || grant.public_path.len() > 2048
        || !is_safe_public_path(&grant.public_path)
        || grant.operation_id.is_empty()
    {
        return Err(StatusCode::FORBIDDEN);
    }
    Ok(grant)
}

fn is_safe_public_path(path: &str) -> bool {
    !path.contains('\0')
        && !path.contains('\r')
        && !path.contains('\n')
        && path.bytes().all(|b| b.is_ascii_graphic())
}

#[cfg(test)]
#[path = "../../tests/unit/transfer_ticket.rs"]
mod tests;
