use prost::Message;
use std::sync::Arc;
use std::time::Duration;

use crate::config::Config;
use crate::infra::kafka::transport_proto::ZoneMetadataSnapshotV1;
use crate::infra::kafka::{KafkaSettlement, KafkaTransport};
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

/// [COMMENT]: Metadata topic là compacted per-Zone; cold start đọc snapshot mới nhất rồi project vào Zone NATS KV.
pub fn start_metadata_event_listener(
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    config: Arc<Config>,
) {
    tokio::spawn(async move {
        let topic = kafka.metadata_topic(&config.zone_id);
        let (consumer, fence) = match kafka
            .consumer(
                format!("aurora-zone-metadata-{}-v1", config.zone_id),
                &topic,
                8,
            )
            .await
        {
            Ok(value) => value,
            Err(error) => {
                Logger::sys_error(
                    "zone_gateway.metadata",
                    "Không thể khởi tạo Kafka metadata consumer",
                    &error,
                );
                return;
            }
        };
        let settlement = KafkaSettlement::new(consumer.clone(), fence.clone());

        loop {
            let records = match consumer.poll(Duration::from_secs(1)).await {
                Ok(records) => records,
                Err(error) => {
                    Logger::sys_error(
                        "zone_gateway.metadata",
                        "Kafka metadata poll thất bại",
                        &error.to_string(),
                    );
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    continue;
                }
            };
            let epoch = fence.epoch();
            for record in records {
                settlement
                    .register(epoch, &record.topic, record.partition, record.offset)
                    .await;
                let Some(value) = record.value else {
                    let dlq = crate::infra::kafka::transport_proto::DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: "ZONE_METADATA_TOMBSTONE_UNEXPECTED".to_string(),
                        error_message: "metadata snapshot topic received tombstone".to_string(),
                        original_payload: Vec::new(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let key = dlq.event_id.clone();
                    if kafka
                        .publish_message(&kafka.dead_letter_topic(), &key, &dlq)
                        .await
                        .is_ok()
                    {
                        let delivery = crate::infra::kafka::KafkaDelivery::new(
                            record.topic,
                            record.partition,
                            record.offset,
                            epoch,
                            settlement.clone(),
                        );
                        let _ = delivery.settle().await;
                    }
                    continue;
                };
                let snapshot = match ZoneMetadataSnapshotV1::decode(value.as_ref()) {
                    Ok(snapshot)
                        if snapshot.schema_version == 1
                            && snapshot.zone_id
                                == uuid::Uuid::parse_str(&config.zone_id)
                                    .map(|value| value.as_bytes().to_vec())
                                    .unwrap_or_default() =>
                    {
                        snapshot
                    }
                    _ => {
                        // [COMMENT]: Poison metadata được DLQ bền vững để compacted partition không kẹt vĩnh viễn.
                        let dlq = crate::infra::kafka::transport_proto::DeadLetterRecordV1 {
                            event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                            source_topic: record.topic.clone(),
                            source_partition: record.partition,
                            source_offset: record.offset,
                            error_code: "ZONE_METADATA_PROTO_INVALID".to_string(),
                            error_message: "ZoneMetadataSnapshotV1 failed strict Zone validation"
                                .to_string(),
                            original_payload: value.to_vec(),
                            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                            schema_version: 1,
                        };
                        let key = dlq.event_id.clone();
                        if kafka
                            .publish_message(&kafka.dead_letter_topic(), &key, &dlq)
                            .await
                            .is_ok()
                        {
                            let delivery = crate::infra::kafka::KafkaDelivery::new(
                                record.topic,
                                record.partition,
                                record.offset,
                                epoch,
                                settlement.clone(),
                            );
                            let _ = delivery.settle().await;
                        }
                        continue;
                    }
                };

                let mut applied = zone_kv
                    .update_zone_metadata(Some(&snapshot.status), None)
                    .await
                    .is_ok();
                for service in snapshot.services {
                    applied = applied
                        && zone_kv
                            .update_zone_metadata(
                                None,
                                Some((&service.service_type, service.enabled)),
                            )
                            .await
                            .is_ok();
                }
                if applied {
                    let delivery = crate::infra::kafka::KafkaDelivery::new(
                        record.topic,
                        record.partition,
                        record.offset,
                        epoch,
                        settlement.clone(),
                    );
                    let _ = delivery.settle().await;
                }
            }
        }
    });
}
