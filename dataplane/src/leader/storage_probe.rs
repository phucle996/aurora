use bytes::Bytes;
use serde::Serialize;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::session::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};

#[derive(Serialize)]
struct StorageHealthSnapshot<'a> {
    status: &'a str,
    capacity: usize,
    updated_at: u64,
    probe_node_id: &'a str,
    fencing_token: u64,
}

pub(crate) async fn run_storage_health_probe(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
) {
    loop {
        let metadata = match zone_kv.read_zone_metadata().await {
            Ok(metadata) => metadata,
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.storage_health_probe",
                    "STORAGE_ZONE_METADATA_READ_FAILED",
                    "Cannot determine whether storage probing is enabled; skipping probe fail-closed",
                    &error,
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        retryable: Some(true),
                        outcome: Some("skipped"),
                        ..LogFields::default()
                    },
                );
                if !session.wait(Duration::from_secs(1)).await {
                    return;
                }
                continue;
            }
        };
        let disabled = metadata.status == "disabled"
            || !metadata.services.get("storage").copied().unwrap_or(false);
        let (status, capacity) = if disabled {
            ("down", 0)
        } else if !session.permits_external_side_effect().await {
            return;
        } else {
            match (&config.minio_host, config.minio_port) {
                (Some(host), Some(port)) => {
                    let cancel = session.cancellation_token();
                    let result = tokio::select! {
                        _ = cancel.cancelled() => return,
                        result = fetch_minio_liveness_http_response(host, port) => result,
                    };
                    match result {
                        Ok(response) if response.contains("200 OK") => ("healthy", 100),
                        Ok(_) => ("degraded", 50),
                        Err(error) => {
                            Logger::sys_warn(
                                "leader.storage_health_probe",
                                "MinIO liveness probe thất bại",
                                &error,
                            );
                            ("down", 0)
                        }
                    }
                }
                _ => ("unknown", 0),
            }
        };

        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_secs())
            .unwrap_or_default();
        let snapshot = StorageHealthSnapshot {
            status,
            capacity,
            updated_at: now,
            probe_node_id: session.owner_id(),
            fencing_token: session.fencing_token(),
        };
        if session.permits_external_side_effect().await {
            match serde_json::to_vec(&snapshot) {
                Ok(value) => {
                    if let Err(error) = zone_kv
                        .health_put_fenced(
                            "zone.service.storage",
                            Bytes::from(value),
                            session.fencing_token(),
                        )
                        .await
                    {
                        Logger::sys_warn_with_fields(
                            "leader.storage_health_probe",
                            "STORAGE_ZONE_HEALTH_WRITE_FAILED",
                            "Could not write fenced Zone storage health snapshot",
                            &error,
                            LogFields {
                                leader_fencing_token: Some(session.fencing_token()),
                                retryable: Some(true),
                                outcome: Some("stale"),
                                ..LogFields::default()
                            },
                        );
                    }
                }
                Err(error) => Logger::sys_error_with_fields(
                    "leader.storage_health_probe",
                    "STORAGE_ZONE_HEALTH_SERIALIZE_FAILED",
                    "Could not serialize Zone storage health snapshot",
                    &error.to_string(),
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        outcome: Some("skipped"),
                        ..LogFields::default()
                    },
                ),
            }
        }

        if !session
            .wait(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000))
            .await
        {
            return;
        }
    }
}

async fn fetch_minio_liveness_http_response(host: &str, port: u16) -> Result<String, String> {
    let mut stream = tokio::time::timeout(
        Duration::from_secs(2),
        tokio::net::TcpStream::connect(format!("{host}:{port}")),
    )
    .await
    .map_err(|_| "Connect timeout".to_string())?
    .map_err(|error| error.to_string())?;
    stream
        .write_all(
            format!("GET /minio/health/live HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n")
                .as_bytes(),
        )
        .await
        .map_err(|error| error.to_string())?;
    let mut response = String::new();
    tokio::time::timeout(Duration::from_secs(2), stream.read_to_string(&mut response))
        .await
        .map_err(|_| "Read timeout".to_string())?
        .map_err(|error| error.to_string())?;
    Ok(response)
}
