use super::db;
use crate::config::Config;
use crate::infra::kafka::transport_proto::{DeadLetterRecordV1, StorageBucketSizesSnapshotV1};
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use prost::Message;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

/// [COMMENT]: Snapshot Protobuf Kafka thay cặp Redis event/snapshot streams; DB update idempotent theo bucket.
pub async fn run_bucket_sizes_listener(
    config: &Config,
    kafka: Arc<KafkaTransport>,
    nats_client: &async_nats::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let topic = kafka.storage_sizes_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-storage-sizes-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
    let mut previous_by_zone: HashMap<String, HashMap<String, i64>> = HashMap::new();

    loop {
        let records = consumer.poll(Duration::from_secs(1)).await?;
        for record in records {
            let payload = record.value.unwrap_or_default();
            let snapshot = match StorageBucketSizesSnapshotV1::decode(payload.as_ref()) {
                Ok(snapshot)
                    if snapshot.schema_version == 1
                        && snapshot.event_id.len() == 16
                        && snapshot.zone_id.len() == 16
                        && snapshot.buckets.iter().all(|bucket| {
                            bucket.size_bytes >= 0
                                && (bucket.bucket_name.starts_with("ws-")
                                    || bucket.bucket_name.starts_with("tn-"))
                        }) =>
                {
                    snapshot
                }
                _ => {
                    // [COMMENT]: Poison snapshot được DLQ trước khi commit để không kẹt partition vĩnh viễn.
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: "STORAGE_SIZES_PROTO_INVALID".to_string(),
                        error_message: "StorageBucketSizesSnapshotV1 failed strict validation"
                            .to_string(),
                        original_payload: payload.to_vec(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let key = dlq.event_id.clone();
                    kafka
                        .publish_message(&kafka.dead_letter_topic(), &key, &dlq)
                        .await
                        .map_err(std::io::Error::other)?;
                    kafka
                        .commit(
                            &consumer,
                            &record.topic,
                            record.partition,
                            record.offset + 1,
                        )
                        .await
                        .map_err(std::io::Error::other)?;
                    continue;
                }
            };
            let zone_id = uuid::Uuid::from_slice(&snapshot.zone_id)?.to_string();
            let current = snapshot
                .buckets
                .into_iter()
                .map(|bucket| (bucket.bucket_name, bucket.size_bytes))
                .collect::<HashMap<_, _>>();
            let previous = previous_by_zone.get(&zone_id);
            let mut user_changed_buckets: HashMap<String, HashMap<String, i64>> = HashMap::new();
            let mut processing_failed = false;

            for (bucket_name, size_bytes) in &current {
                let old_size = previous.and_then(|sizes| sizes.get(bucket_name)).copied();
                if old_size == Some(*size_bytes) {
                    continue;
                }
                let mut target_user_ids = Vec::new();
                if bucket_name.starts_with("ws-") {
                    match db::update_personal_bucket_size(
                        &config.database_url,
                        bucket_name,
                        *size_bytes,
                    )
                    .await
                    {
                        Ok(Some(owner_id)) => {
                            target_user_ids.push(owner_id.clone());
                            let billing_payload = serde_json::to_vec(&serde_json::json!({
                                "bucket_name": bucket_name,
                                "owner_id": owner_id,
                                "owner_type": "personal",
                                "used_bytes": size_bytes,
                                "timestamp": snapshot.observed_at_unix_ms
                            }))?;
                            if nats_client
                                .publish(
                                    "billing.storage.bucket_used_bytes_update",
                                    billing_payload.into(),
                                )
                                .await
                                .is_err()
                            {
                                processing_failed = true;
                                break;
                            }
                        }
                        Ok(None) => {}
                        Err(error) => {
                            Logger::sys_error(
                                "storage_sizes.personal",
                                "Personal bucket size DB update failed",
                                &error.to_string(),
                            );
                            processing_failed = true;
                            break;
                        }
                    }
                } else if bucket_name.starts_with("tn-") {
                    match db::update_tenant_bucket_size(
                        &config.database_url,
                        bucket_name,
                        *size_bytes,
                    )
                    .await
                    {
                        Ok(user_ids) => target_user_ids = user_ids,
                        Err(error) => {
                            Logger::sys_error(
                                "storage_sizes.tenant",
                                "Tenant bucket size DB update failed",
                                &error.to_string(),
                            );
                            processing_failed = true;
                            break;
                        }
                    }
                }

                if old_size.is_none()
                    || old_size.is_some_and(|old| (size_bytes - old).abs() >= 1_048_576)
                {
                    for user_id in target_user_ids {
                        user_changed_buckets
                            .entry(user_id)
                            .or_default()
                            .insert(bucket_name.clone(), *size_bytes);
                    }
                }
            }
            if processing_failed {
                // [COMMENT]: Không xử lý record tiếp theo trong partition khi side effect hiện tại lỗi,
                // nếu không commit offset cao hơn sẽ làm mất snapshot chưa hoàn tất.
                return Err("storage size snapshot side effect failed".into());
            }

            for (user_id, sizes) in user_changed_buckets {
                nats_client
                    .publish(
                        format!("storage.bucket.sizes.sync.{user_id}"),
                        serde_json::to_vec(&serde_json::json!({ "sizes": sizes }))?.into(),
                    )
                    .await?;
            }

            // [COMMENT]: Cập nhật previous snapshot sau toàn bộ DB/NATS side effect và trước offset commit.
            previous_by_zone.insert(zone_id, current);
            kafka
                .commit(
                    &consumer,
                    &record.topic,
                    record.partition,
                    record.offset + 1,
                )
                .await
                .map_err(std::io::Error::other)?;
        }
    }
}
