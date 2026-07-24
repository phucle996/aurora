use bytes::Bytes;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::super::leadership::ZoneLeaderSession;
use crate::config::Config;
use crate::executor::storage::core::client::MinioClient;
use crate::infra::kafka::transport_proto::{BucketSizeV1, StorageBucketSizesSnapshotV1};
use crate::infra::kafka::KafkaTransport;
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

pub(crate) async fn run_storage_bucket_size_scanner(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
) {
    Logger::sys_info(
        "leader.storage_bucket_size_scanner",
        "Zone leader phụ trách quét dung lượng MinIO mỗi 15s",
    );

    loop {
        if !session.wait(Duration::from_secs(15)).await {
            return;
        }
        let metadata = match zone_kv.read_zone_metadata().await {
            Ok(metadata) => metadata,
            Err(error) => {
                // [COMMENT]: Desired state không đọc được thì không được suy diễn storage enabled.
                Logger::sys_warn(
                    "leader.storage_bucket_size_scanner",
                    "Không đọc được Zone metadata; bỏ qua scan theo fail-closed",
                    &error,
                );
                continue;
            }
        };
        let storage_enabled = metadata.services.get("storage").copied().unwrap_or(false);
        if metadata.status != "active" || !storage_enabled {
            continue;
        }
        if !session.permits_external_side_effect().await {
            return;
        }

        let cancel = session.cancellation_token();
        let bucket_sizes = tokio::select! {
            _ = cancel.cancelled() => return,
            result = scan_all_customer_storage_bucket_sizes() => result,
        };
        let bucket_sizes = match bucket_sizes {
            Ok(sizes) => sizes,
            Err(error) => {
                Logger::sys_error(
                    "leader.storage_bucket_size_scanner",
                    "Không thể hoàn thành MinIO bucket-size scan",
                    &error,
                );
                continue;
            }
        };
        if bucket_sizes.is_empty() || !session.permits_external_side_effect().await {
            continue;
        }

        let event_id = uuid::Uuid::new_v4();
        let snapshot = StorageBucketSizesSnapshotV1 {
            event_id: event_id.as_bytes().to_vec(),
            zone_id: uuid::Uuid::parse_str(&config.zone_id)
                .map(|value| value.as_bytes().to_vec())
                .unwrap_or_default(),
            observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            buckets: bucket_sizes
                .into_iter()
                .map(|(bucket_name, size_bytes)| BucketSizeV1 {
                    bucket_name,
                    size_bytes,
                })
                .collect(),
            schema_version: 1,
        };
        if let Err(error) = kafka
            .publish_message(
                &kafka.storage_sizes_topic(),
                config.zone_id.as_bytes(),
                &snapshot,
            )
            .await
        {
            Logger::sys_error(
                "leader.storage_bucket_size_scanner",
                "Không thể publish storage size snapshot",
                &error,
            );
        }
    }
}

async fn scan_all_customer_storage_bucket_sizes() -> Result<HashMap<String, i64>, String> {
    let minio_client = MinioClient::from_env_private().await;
    let s3 = minio_client.s3();
    let buckets = crate::observability::otel::OtelTracer::trace_result(
        "S3 ListBuckets",
        opentelemetry::trace::SpanKind::Client,
        vec![
            opentelemetry::KeyValue::new("rpc.system", "aws-api"),
            opentelemetry::KeyValue::new("rpc.service", "S3"),
            opentelemetry::KeyValue::new("rpc.method", "ListBuckets"),
        ],
        s3.list_buckets().send(),
    )
    .await
    .map_err(|error| format!("list buckets failed: {error}"))?
    .buckets
    .unwrap_or_default();
    let mut bucket_sizes = HashMap::new();

    for bucket in buckets {
        let Some(name) = bucket.name else {
            continue;
        };
        if !name.starts_with("ws-") && !name.starts_with("tn-") {
            continue;
        }
        let mut total_size = 0_i64;
        let mut continuation_token = None;
        loop {
            let mut request = s3.list_objects_v2().bucket(&name);
            if let Some(token) = &continuation_token {
                request = request.continuation_token(token);
            }
            let response = crate::observability::otel::OtelTracer::trace_result(
                "S3 ListObjectsV2",
                opentelemetry::trace::SpanKind::Client,
                vec![
                    opentelemetry::KeyValue::new("rpc.system", "aws-api"),
                    opentelemetry::KeyValue::new("rpc.service", "S3"),
                    opentelemetry::KeyValue::new("rpc.method", "ListObjectsV2"),
                ],
                request.send(),
            )
            .await
            .map_err(|error| format!("list objects for {name} failed: {error}"))?;
            for object in response.contents.unwrap_or_default() {
                total_size = total_size.saturating_add(object.size.unwrap_or_default());
            }
            if response.is_truncated.unwrap_or(false) {
                continuation_token = response.next_continuation_token;
            } else {
                break;
            }
        }
        bucket_sizes.insert(name, total_size);
    }
    Ok(bucket_sizes)
}
