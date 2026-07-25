use super::store;
use crate::config::Config;
use crate::infra::kafka::transport_proto::{DeadLetterRecordV1, StorageBucketSizesSnapshotV1};
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use prost::Message;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::NoTls;

const REALTIME_CHANNEL: &str = "aurora:realtime:notifications";

/// [COMMENT]: Snapshot Protobuf Kafka thay cặp Redis event/snapshot streams; DB update idempotent theo bucket.
pub async fn run_bucket_sizes_listener(
    config: &Config,
    kafka: Arc<KafkaTransport>,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let (pg_client, pg_connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;
    tokio::spawn(async move {
        if let Err(error) = pg_connection.await {
            Logger::sys_error(
                "storage_usage.postgres",
                "Storage usage PostgreSQL connection stopped",
                &error.to_string(),
            );
        }
    });
    pg_client
        .batch_execute(
            "SET statement_timeout = '5s'; \
             SET lock_timeout = '1s'; \
             SET idle_in_transaction_session_timeout = '5s'",
        )
        .await?;
    let mut redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    let topic = kafka.storage_sizes_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-storage-sizes-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
    loop {
        let records = consumer.poll(Duration::from_secs(1)).await?;
        for record in records {
            let payload = record.value.unwrap_or_default();
            let snapshot = match (payload.len() <= 8 * 1024 * 1024)
                .then(|| StorageBucketSizesSnapshotV1::decode(payload.as_ref()))
            {
                Some(Ok(snapshot))
                    if snapshot.schema_version == 1
                        && snapshot.event_id.len() == 16
                        && snapshot.zone_id.len() == 16
                        && snapshot.buckets.len() <= 50_000
                        && snapshot.buckets.iter().all(|bucket| {
                            bucket.size_bytes >= 0
                                && !bucket.bucket_name.is_empty()
                                && bucket.bucket_name.len() <= 128
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
                        original_payload: payload.iter().take(4_096).copied().collect(),
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
            let _zone_id = uuid::Uuid::from_slice(&snapshot.zone_id)?.to_string();
            let current = snapshot
                .buckets
                .into_iter()
                .map(|bucket| (bucket.bucket_name, bucket.size_bytes))
                .collect::<HashMap<_, _>>();
            let mut user_changed_buckets: HashMap<String, HashMap<String, i64>> = HashMap::new();
            let mut processing_failed = false;

            for (bucket_name, size_bytes) in &current {
                let mut target_user_ids = Vec::new();
                if bucket_name.starts_with("ws-") {
                    match store::update_personal_bucket_size(&pg_client, bucket_name, *size_bytes)
                        .await
                    {
                        Ok(Some(owner_id)) => {
                            target_user_ids.push(owner_id);
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
                    match store::update_tenant_bucket_size(&pg_client, bucket_name, *size_bytes)
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

                for user_id in target_user_ids {
                    user_changed_buckets
                        .entry(user_id)
                        .or_default()
                        .insert(bucket_name.clone(), *size_bytes);
                }
            }
            if processing_failed {
                // [COMMENT]: Không xử lý record tiếp theo trong partition khi side effect hiện tại lỗi,
                // nếu không commit offset cao hơn sẽ làm mất snapshot chưa hoàn tất.
                return Err("storage size snapshot side effect failed".into());
            }

            for (user_id, sizes) in user_changed_buckets {
                let envelope = serde_json::to_vec(&serde_json::json!({
                    "kind": "storage",
                    "user_id": user_id,
                    "payload": { "sizes": sizes }
                }))?;
                // UI wake-up is soft state. PostgreSQL is already authoritative,
                // so Redis Pub/Sub failure must not replay durable bucket updates.
                if let Err(error) = redis_conn
                    .publish::<_, _, i64>(REALTIME_CHANNEL, envelope)
                    .await
                {
                    Logger::sys_warn(
                        "storage_usage.notify",
                        "Storage usage notification dropped; UI recovers via authoritative API",
                        &error.to_string(),
                    );
                }
            }

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
