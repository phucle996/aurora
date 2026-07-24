use prost::Message;
use std::sync::Arc;
use std::time::Duration;

use super::leadership::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::kafka::transport_proto::{
    DeadLetterRecordV1, ZoneMetadataQueryV1, ZoneMetadataSnapshotV1,
};
use crate::infra::kafka::{KafkaDelivery, KafkaSettlement, KafkaTransport};
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};

/// [COMMENT]: Leader duy nhất consume compacted metadata rồi project vào Zone NATS KV dùng chung.
pub(crate) async fn run_zone_metadata_kafka_listener(
    session: ZoneLeaderSession,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    config: Arc<Config>,
) {
    let topic = kafka.metadata_topic(&config.zone_id);
    while !session.is_cancelled() {
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
                    "leader.zone_metadata_kafka_listener",
                    "Không thể khởi tạo Kafka metadata consumer",
                    &error,
                );
                if !session.wait(Duration::from_secs(2)).await {
                    return;
                }
                continue;
            }
        };
        let settlement = KafkaSettlement::new(consumer.clone(), fence.clone());

        while !session.is_cancelled() {
            let records = match consumer.poll(Duration::from_secs(1)).await {
                Ok(records) => records,
                Err(error) => {
                    Logger::sys_error(
                        "leader.zone_metadata_kafka_listener",
                        "Kafka metadata poll thất bại",
                        &error.to_string(),
                    );
                    if !session.wait(Duration::from_secs(1)).await {
                        return;
                    }
                    continue;
                }
            };
            // [COMMENT]: Không project/settle record sau khi session bị fence; leader kế tiếp replay.
            if !session.permits_external_side_effect().await {
                return;
            }
            let epoch = fence.epoch();
            for record in records {
                settlement
                    .register(epoch, &record.topic, record.partition, record.offset)
                    .await;
                let delivery = KafkaDelivery::new(
                    record.topic.clone(),
                    record.partition,
                    record.offset,
                    epoch,
                    settlement.clone(),
                );
                let Some(value) = record.value else {
                    let error_code = "ZONE_METADATA_TOMBSTONE_UNEXPECTED";
                    let dlq = DeadLetterRecordV1 {
                        event_id: stable_metadata_dlq_event_id(
                            &record.topic,
                            record.partition,
                            record.offset,
                            error_code,
                        ),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: error_code.to_string(),
                        error_message: "metadata snapshot topic received tombstone".to_string(),
                        original_payload: Vec::new(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    quarantine_metadata_record(&session, kafka.as_ref(), &delivery, dlq, epoch)
                        .await;
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
                        let error_code = "ZONE_METADATA_PROTO_INVALID";
                        let dlq = DeadLetterRecordV1 {
                            event_id: stable_metadata_dlq_event_id(
                                &record.topic,
                                record.partition,
                                record.offset,
                                error_code,
                            ),
                            source_topic: record.topic.clone(),
                            source_partition: record.partition,
                            source_offset: record.offset,
                            error_code: error_code.to_string(),
                            error_message: "ZoneMetadataSnapshotV1 failed strict Zone validation"
                                .to_string(),
                            original_payload: value.to_vec(),
                            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                            schema_version: 1,
                        };
                        quarantine_metadata_record(&session, kafka.as_ref(), &delivery, dlq, epoch)
                            .await;
                        continue;
                    }
                };

                let mut apply_error = zone_kv
                    .update_zone_metadata(Some(&snapshot.status), None)
                    .await
                    .err();
                for service in snapshot.services {
                    if apply_error.is_none() {
                        apply_error = zone_kv
                            .update_zone_metadata(
                                None,
                                Some((&service.service_type, service.enabled)),
                            )
                            .await
                            .err();
                    }
                }
                if let Some(error) = apply_error {
                    Logger::sys_error_with_fields(
                        "leader.zone_metadata_kafka_listener",
                        "ZONE_METADATA_KV_APPLY_FAILED",
                        "Zone metadata projection was not fully applied; Kafka source remains unsettled",
                        &error,
                        LogFields {
                            leader_fencing_token: Some(session.fencing_token()),
                            kafka_topic: Some(&record.topic),
                            kafka_partition: Some(record.partition),
                            kafka_offset: Some(record.offset),
                            assignment_epoch: Some(epoch),
                            retryable: Some(true),
                            outcome: Some("unsettled"),
                            ..LogFields::default()
                        },
                    );
                    continue;
                }
                if let Err(error) = delivery.settle().await {
                    Logger::sys_error_with_fields(
                        "leader.zone_metadata_kafka_listener",
                        "ZONE_METADATA_SOURCE_SETTLEMENT_FAILED",
                        "Zone metadata projection applied but Kafka settlement failed; idempotent replay is expected",
                        &error,
                        LogFields {
                            leader_fencing_token: Some(session.fencing_token()),
                            kafka_topic: Some(&record.topic),
                            kafka_partition: Some(record.partition),
                            kafka_offset: Some(record.offset),
                            assignment_epoch: Some(epoch),
                            retryable: Some(true),
                            outcome: Some("replay_expected"),
                            ..LogFields::default()
                        },
                    );
                }
            }
        }
    }
}

/// [COMMENT]: Cold-start và repair query thuộc leader session; không cần lease con cho từng duty.
pub(crate) async fn run_zone_metadata_repair_publisher(
    session: ZoneLeaderSession,
    kafka: Arc<KafkaTransport>,
    config: Arc<Config>,
) {
    loop {
        if session.permits_external_side_effect().await {
            let request_id = uuid::Uuid::new_v4();
            let query = ZoneMetadataQueryV1 {
                request_id: request_id.as_bytes().to_vec(),
                zone_id: uuid::Uuid::parse_str(&config.zone_id)
                    .map(|value| value.as_bytes().to_vec())
                    .unwrap_or_default(),
                requested_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                schema_version: 1,
            };
            if let Err(error) = kafka
                .publish_message(
                    &kafka.metadata_query_topic(),
                    config.zone_id.as_bytes(),
                    &query,
                )
                .await
            {
                Logger::sys_warn(
                    "leader.zone_metadata_repair_publisher",
                    "Không publish được Zone metadata repair query",
                    &error,
                );
            }
        }

        // [COMMENT]: Shared compacted snapshot tự repair realtime; full query chỉ cold-start/hourly.
        if !session
            .wait(Duration::from_secs(60 * 60 + rand::random::<u64>() % 30))
            .await
        {
            return;
        }
    }
}

fn stable_metadata_dlq_event_id(
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
) -> Vec<u8> {
    uuid::Uuid::new_v5(
        &uuid::Uuid::NAMESPACE_OID,
        format!("{source_topic}\0{source_partition}\0{source_offset}\0{error_code}").as_bytes(),
    )
    .as_bytes()
    .to_vec()
}

async fn quarantine_metadata_record(
    session: &ZoneLeaderSession,
    kafka: &KafkaTransport,
    delivery: &KafkaDelivery,
    dlq: DeadLetterRecordV1,
    assignment_epoch: u64,
) {
    let event_id = uuid::Uuid::from_slice(&dlq.event_id)
        .map(|value| value.to_string())
        .unwrap_or_default();
    let fields = || LogFields {
        event_id: Some(event_id.as_str()),
        leader_fencing_token: Some(session.fencing_token()),
        kafka_topic: Some(dlq.source_topic.as_str()),
        kafka_partition: Some(dlq.source_partition),
        kafka_offset: Some(dlq.source_offset),
        assignment_epoch: Some(assignment_epoch),
        outcome: Some("quarantined"),
        ..LogFields::default()
    };
    let key = dlq.event_id.clone();
    match kafka
        .publish_message(&kafka.dead_letter_topic(), &key, &dlq)
        .await
    {
        Ok(()) => match delivery.settle().await {
            Ok(()) => Logger::sys_warn_with_fields(
                "leader.zone_metadata_kafka_listener",
                &dlq.error_code,
                "Invalid Zone metadata record was durably quarantined before source settlement",
                "",
                fields(),
            ),
            Err(error) => Logger::sys_error_with_fields(
                "leader.zone_metadata_kafka_listener",
                "ZONE_METADATA_DLQ_SETTLEMENT_FAILED",
                "Metadata DLQ publish succeeded but source settlement failed; replay is expected",
                &error,
                LogFields {
                    retryable: Some(true),
                    outcome: Some("replay_expected"),
                    ..fields()
                },
            ),
        },
        Err(error) => Logger::sys_error_with_fields(
            "leader.zone_metadata_kafka_listener",
            "ZONE_METADATA_DLQ_PUBLISH_FAILED",
            "Invalid metadata source remains unsettled because durable DLQ publish failed",
            &error,
            LogFields {
                retryable: Some(true),
                outcome: Some("unsettled"),
                ..fields()
            },
        ),
    }
}
