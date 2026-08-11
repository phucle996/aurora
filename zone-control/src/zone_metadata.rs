use std::sync::Arc;
use std::time::Duration;

use prost::Message;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::transfer_ticket::config::Config;
use crate::transport_proto::{DeadLetterRecordV1, ZoneMetadataQueryV1, ZoneMetadataSnapshotV1};
use crate::zone_control_kafka::ControlKafka;
use crate::zone_control_state::ZoneControlState;

pub(crate) async fn run_projection(
    config: Config,
    state: Arc<ZoneControlState>,
    kafka: Arc<ControlKafka>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.metadata_projection.0";
    let expected_zone = Uuid::parse_str(&config.zone_id)
        .map_err(|_| "ZONE_ID is invalid for metadata projection".to_string())?;
    let topic = kafka.metadata_topic(&config.zone_id);
    while !shutdown.is_cancelled() {
        let (consumer, fence) = match kafka
            .consumer(
                format!("aurora-zone-control-metadata-{}-v1", config.zone_id),
                &topic,
                8,
            )
            .await
        {
            Ok(value) => value,
            Err(error) => {
                tracing::warn!(
                    event_code = "ZONE_CONTROL_METADATA_CONSUMER_UNAVAILABLE",
                    error = %error,
                    retryable = true
                );
                if !wait_or_cancel(&shutdown, Duration::from_secs(2)).await {
                    return Ok(());
                }
                continue;
            }
        };
        loop {
            if shutdown.is_cancelled() {
                return Ok(());
            }
            let records = match consumer.poll(Duration::from_secs(1)).await {
                Ok(records) => records,
                Err(error) => {
                    tracing::warn!(
                        event_code = "ZONE_CONTROL_METADATA_POLL_FAILED",
                        error = %error,
                        retryable = true
                    );
                    if !wait_or_cancel(&shutdown, Duration::from_secs(1)).await {
                        return Ok(());
                    }
                    continue;
                }
            };
            let epoch = fence.epoch();
            for record in records {
                if shutdown.is_cancelled()
                    || fence.epoch() != epoch
                    || !state
                        .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
                        .await?
                {
                    return Ok(());
                }
                let Some(value) = record.value else {
                    let dlq = invalid_record(
                        &record.topic,
                        record.partition,
                        record.offset,
                        "ZONE_METADATA_TOMBSTONE_UNEXPECTED",
                        Vec::new(),
                    );
                    kafka
                        .publish_proto(&kafka.dead_letter_topic(), &dlq.event_id, &dlq)
                        .await?;
                    kafka
                        .commit(
                            &consumer,
                            &fence,
                            epoch,
                            &record.topic,
                            record.partition,
                            record.offset,
                        )
                        .await?;
                    continue;
                };
                let snapshot = match ZoneMetadataSnapshotV1::decode(value.as_ref()) {
                    Ok(snapshot)
                        if snapshot.schema_version == 1
                            && snapshot.zone_id == expected_zone.as_bytes() =>
                    {
                        snapshot
                    }
                    _ => {
                        let dlq = invalid_record(
                            &record.topic,
                            record.partition,
                            record.offset,
                            "ZONE_METADATA_PROTO_INVALID",
                            value.to_vec(),
                        );
                        kafka
                            .publish_proto(&kafka.dead_letter_topic(), &dlq.event_id, &dlq)
                            .await?;
                        kafka
                            .commit(
                                &consumer,
                                &fence,
                                epoch,
                                &record.topic,
                                record.partition,
                                record.offset,
                            )
                            .await?;
                        continue;
                    }
                };
                state
                    .update_metadata(Some(&snapshot.status), None)
                    .await
                    .map_err(|error| format!("apply Zone metadata status: {error}"))?;
                for service in snapshot.services {
                    state
                        .update_metadata(None, Some((&service.service_type, service.enabled)))
                        .await
                        .map_err(|error| format!("apply Zone metadata service: {error}"))?;
                }
                kafka
                    .commit(
                        &consumer,
                        &fence,
                        epoch,
                        &record.topic,
                        record.partition,
                        record.offset,
                    )
                    .await?;
            }
        }
    }
    Ok(())
}

pub(crate) async fn run_repair_publisher(
    config: Config,
    state: Arc<ZoneControlState>,
    kafka: Arc<ControlKafka>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.metadata_repair.0";
    let zone_id = Uuid::parse_str(&config.zone_id)
        .map_err(|_| "ZONE_ID is invalid for metadata repair".to_string())?;
    loop {
        tokio::select! {
            _ = shutdown.cancelled() => return Ok(()),
            _ = tokio::time::sleep(Duration::from_secs(60 * 60 + rand::random::<u64>() % 30)) => {
                if !state.assignment_is_current(ASSIGNMENT_KEY, assignment_epoch).await? {
                    continue;
                }
                let query = ZoneMetadataQueryV1 {
                    request_id: Uuid::new_v4().as_bytes().to_vec(),
                    zone_id: zone_id.as_bytes().to_vec(),
                    requested_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                    schema_version: 1,
                };
                kafka
                    .publish_proto(&kafka.metadata_query_topic(), config.zone_id.as_bytes(), &query)
                    .await?;
            }
        }
    }
}

fn invalid_record(
    topic: &str,
    partition: i32,
    offset: i64,
    error_code: &'static str,
    original_payload: Vec<u8>,
) -> DeadLetterRecordV1 {
    let event_id = Uuid::new_v5(
        &Uuid::NAMESPACE_OID,
        format!("{topic}\0{partition}\0{offset}\0{error_code}").as_bytes(),
    );
    DeadLetterRecordV1 {
        event_id: event_id.as_bytes().to_vec(),
        source_topic: topic.to_string(),
        source_partition: partition,
        source_offset: offset,
        error_code: error_code.to_string(),
        error_message: error_code.to_string(),
        original_payload,
        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        schema_version: 1,
    }
}

async fn wait_or_cancel(shutdown: &CancellationToken, duration: Duration) -> bool {
    tokio::select! {
        _ = shutdown.cancelled() => false,
        _ = tokio::time::sleep(duration) => true,
    }
}
