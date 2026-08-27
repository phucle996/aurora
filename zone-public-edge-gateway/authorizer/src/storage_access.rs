use std::{
    collections::HashMap,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_nats::jetstream;
use bytes::Bytes;
use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tokio::sync::Semaphore;
use tonic::Status;
use uuid::Uuid;

use crate::transfer_proto;

const TRANSFER_TICKET_SCHEMA_VERSION: u32 = 1;

#[derive(Clone)]
pub struct StorageAccessAuthorizer {
    transfer_store: jetstream::kv::Store,
    admission_store: jetstream::kv::Store,
    timeout: Duration,
    zone_id: String,
    inflight: Arc<Semaphore>,
}

#[derive(serde::Deserialize)]
struct AdmissionRecord {
    #[serde(default)]
    resource_id: String,
    #[serde(default)]
    resource_name: String,
    policy_version: i64,
    decision: String,
    effective_at_unix_seconds: i64,
    valid_until_unix_seconds: Option<i64>,
}

impl StorageAccessAuthorizer {
    pub fn new(
        transfer_store: jetstream::kv::Store,
        admission_store: jetstream::kv::Store,
        timeout: Duration,
        zone_id: String,
        max_inflight: usize,
    ) -> Self {
        Self {
            transfer_store,
            admission_store,
            timeout,
            zone_id,
            inflight: Arc::new(Semaphore::new(max_inflight)),
        }
    }

    pub async fn authorize(
        &self,
        headers: &HashMap<String, String>,
        method: &str,
        request_path: &str,
    ) -> Result<CheckResponse, Status> {
        let Ok(_permit) = self.inflight.clone().try_acquire_owned() else {
            return Err(Status::resource_exhausted(
                "Zone Storage authorizer overloaded",
            ));
        };
        let token = headers.get("x-aurora-transfer-ticket");
        let Some(token) = token else {
            // S3 metadata/list/cleanup operations do not add billable object
            // bytes. They remain available while a wallet is suspended so a
            // customer can inspect and remove data. CORS preflight must also
            // pass before it reaches MinIO.
            let path = request_path
                .split('?')
                .next()
                .unwrap_or_default()
                .trim_start_matches('/');
            let is_list_get = is_sdk_bucket_list(method, request_path);
            if matches!(method, "OPTIONS" | "HEAD" | "DELETE") || is_list_get {
                let mut response = CheckResponse::with_status(Status::ok("authorized"));
                response.set_http_response(
                    envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
                );
                return Ok(response);
            }
            if !matches!(method, "GET" | "PUT") {
                return Ok(CheckResponse::with_status(Status::permission_denied(
                    "SDK method denied",
                )));
            }
            let bucket_name = path
                .split('/')
                .next()
                .filter(|value| !value.is_empty() && value.len() <= 255)
                .ok_or_else(|| Status::permission_denied("SDK bucket path missing"))?;
            let admission_entry = tokio::time::timeout(
                self.timeout,
                self.admission_store.entry(format!("name/{bucket_name}")),
            )
            .await
            .map_err(|_| Status::unavailable("Storage admission unavailable"))?
            .map_err(|_| Status::unavailable("Storage admission unavailable"))?;
            let Some(admission_entry) = admission_entry else {
                return Ok(CheckResponse::with_status(Status::permission_denied(
                    "Storage commercial admission missing",
                )));
            };
            let admission: AdmissionRecord = serde_json::from_slice(&admission_entry.value)
                .map_err(|_| Status::unavailable("Storage admission record corrupt"))?;
            let now = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map_err(|_| Status::unavailable("System clock unavailable"))?
                .as_secs() as i64;
            if admission.policy_version <= 0
                || Uuid::parse_str(&admission.resource_id).is_err()
                || admission.resource_name != bucket_name
                || admission.decision != "ALLOW"
                || admission.effective_at_unix_seconds > now
                || admission
                    .valid_until_unix_seconds
                    .is_some_and(|until| until <= now)
            {
                return Ok(CheckResponse::with_status(Status::permission_denied(
                    "Storage commercial admission suspended",
                )));
            }
            let mut response = CheckResponse::with_status(Status::ok("authorized"));
            response.set_http_response(
                envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
            );
            if let Some(
                envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse::OkResponse(
                    ok,
                ),
            ) = response.http_response.as_mut()
            {
                use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
                for (key, value) in [
                    ("x-aurora-resource-id", admission.resource_id),
                    ("x-aurora-metering-module", "storage".to_string()),
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
            }
            return Ok(response);
        };

        let (ticket_id, secret) = token
            .split_once('.')
            .ok_or_else(|| Status::permission_denied("Transfer ticket invalid"))?;
        if ticket_id.is_empty() || secret.len() < 32 {
            return Ok(CheckResponse::with_status(Status::permission_denied(
                "Transfer ticket invalid",
            )));
        }
        let entry = tokio::time::timeout(
            self.timeout,
            self.transfer_store.entry(ticket_id.to_string()),
        )
        .await
        .map_err(|_| Status::unavailable("Transfer ticket store unavailable"))?
        .map_err(|_| Status::unavailable("Transfer ticket store unavailable"))?;
        let Some(entry) = entry else {
            return Ok(CheckResponse::with_status(Status::permission_denied(
                "Transfer ticket invalid",
            )));
        };
        let mut ticket = transfer_proto::TransferTicketV1::decode(entry.value.as_ref())
            .map_err(|_| Status::unavailable("Transfer ticket store corrupt"))?;
        let admission_entry = tokio::time::timeout(
            self.timeout,
            self.admission_store.entry(ticket.resource_id.clone()),
        )
        .await
        .map_err(|_| Status::unavailable("Storage admission unavailable"))?
        .map_err(|_| Status::unavailable("Storage admission unavailable"))?;
        let admission = admission_entry
            .ok_or_else(|| Status::permission_denied("Storage commercial admission missing"))?;
        let admission: AdmissionRecord = serde_json::from_slice(&admission.value)
            .map_err(|_| Status::unavailable("Storage admission record corrupt"))?;
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| Status::unavailable("System clock unavailable"))?
            .as_secs();
        if admission.policy_version <= 0
            || Uuid::parse_str(&admission.resource_id).is_err()
            || admission.resource_id != ticket.resource_id
            || admission.decision != "ALLOW"
            || admission.effective_at_unix_seconds > now as i64
            || admission
                .valid_until_unix_seconds
                .is_some_and(|until| until <= now as i64)
        {
            return Ok(CheckResponse::with_status(Status::permission_denied(
                "Storage commercial admission suspended",
            )));
        }
        let actual_hash = format!("{:x}", Sha256::digest(secret.as_bytes()));
        let length = headers
            .get("content-length")
            .and_then(|value| value.parse::<u64>().ok());
        let content_type = headers.get("content-type");
        if ticket.schema_version != TRANSFER_TICKET_SCHEMA_VERSION
            || ticket.zone_id != self.zone_id
            || ticket.state != transfer_proto::TransferTicketState::Issued as i32
            || ticket.expires_at_unix_seconds <= now
            || ticket
                .secret_sha256
                .as_bytes()
                .ct_eq(actual_hash.as_bytes())
                .unwrap_u8()
                != 1
            || ticket.method != method
            || ticket.public_path != request_path
            || !matches!(method, "GET" | "PUT" | "POST" | "DELETE")
            || (ticket.content_length.is_some() && ticket.content_length != length)
            || (ticket.content_type.is_some()
                && ticket.content_type.as_deref() != content_type.map(String::as_str))
        {
            return Ok(CheckResponse::with_status(Status::permission_denied(
                "Transfer ticket denied",
            )));
        }
        ticket.state = transfer_proto::TransferTicketState::Consuming as i32;
        let value = Bytes::from(ticket.encode_to_vec());
        match tokio::time::timeout(
            self.timeout,
            self.transfer_store.update(ticket_id, value, entry.revision),
        )
        .await
        {
            Err(_) => return Err(Status::unavailable("Transfer ticket consume unavailable")),
            Ok(Err(_)) => {
                return Ok(CheckResponse::with_status(Status::permission_denied(
                    "Transfer ticket already consumed",
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
                ("x-aurora-metering-module", "storage".to_string()),
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
        Ok(response)
    }
}

// Only an exact bucket-root GET is a non-billable SDK list operation. Object
// paths remain admission-gated even when they end in '/' or carry list-like
// query keys, because both are valid object request shapes.
fn is_sdk_bucket_list(method: &str, request_path: &str) -> bool {
    if method != "GET" {
        return false;
    }
    let path = request_path.split('?').next().unwrap_or_default();
    let Some(bucket) = path.strip_prefix('/') else {
        return false;
    };
    let bucket = bucket.strip_suffix('/').unwrap_or(bucket);
    !bucket.is_empty()
        && bucket.len() <= 255
        && !bucket
            .bytes()
            .any(|byte| matches!(byte, b'/' | b'\\' | b'%'))
}

#[cfg(test)]
#[path = "../test/storage_access.rs"]
mod tests;
