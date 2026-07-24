use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use super::session::ZoneLeaderSession;
use crate::config::Config;
use crate::executor::storage::core::client::MinioClient;
use crate::infra::kafka::transport_proto::{BucketSizeV1, StorageBucketSizesSnapshotV1};
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

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
