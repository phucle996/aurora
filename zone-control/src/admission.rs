use std::sync::Arc;
use std::time::Duration;

use prost::Message;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::transfer_ticket::config::Config;
use crate::wallet_admission_proto::WalletAdmissionChangedV1;
use crate::zone_control_kafka::ControlKafka;
use crate::zone_control_state::{StorageAdmission, ZoneControlState};

pub(crate) async fn run_projection(
    config: Config,
    state: Arc<ZoneControlState>,
    kafka: Arc<ControlKafka>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.storage_admission.0";
    let expected_zone = Uuid::parse_str(&config.zone_id)
        .map_err(|_| "ZONE_ID is invalid for Storage admission projection".to_string())?;
    let topic = kafka.storage_wallet_admission_topic(&config.zone_id);
    while !shutdown.is_cancelled() {
        let (consumer, fence) = match kafka
            .consumer(
                format!(
                    "aurora-zone-control-storage-admission-{}-v1",
                    config.zone_id
                ),
                &topic,
                32,
            )
            .await
        {
            Ok(value) => value,
            Err(error) => {
                tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_CONSUMER_UNAVAILABLE", error = %error);
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
            let records = consumer
                .poll(Duration::from_secs(1))
                .await
                .map_err(|error| format!("poll Storage admission Kafka: {error}"))?;
            let epoch = fence.epoch();
            for record in records {
                if fence.epoch() != epoch
                    || !state
                        .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
                        .await?
                {
                    return Ok(());
                }
                let Some(value) = record.value else {
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
                let event = match WalletAdmissionChangedV1::decode(value.as_ref()) {
                    Ok(event)
                        if event.event_id != ""
                            && event.wallet_version > 0
                            && (event.admission_mode == "ALLOW"
                                || event.admission_mode == "SUSPEND_BILLABLE")
                            && ((event.admission_mode == "ALLOW"
                                && event.restriction_reason.is_empty())
                                || (event.admission_mode == "SUSPEND_BILLABLE"
                                    && !event.restriction_reason.is_empty()))
                            && (event.owner_type == "PERSONAL" || event.owner_type == "TENANT")
                            && event.storage_targets.len() == 1 =>
                    {
                        event
                    }
                    _ => {
                        tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_EVENT_INVALID", topic = %record.topic, partition = record.partition, offset = record.offset);
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
                let target = &event.storage_targets[0];
                if target.zone_id != expected_zone.to_string()
                    || Uuid::parse_str(&target.resource_id).is_err()
                    || target.resource_name.is_empty()
                    || Uuid::parse_str(&event.event_id).is_err()
                {
                    tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_EVENT_SCOPE_REJECTED");
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
                let effective_at = match chrono::DateTime::parse_from_rfc3339(&event.effective_at) {
                    Ok(parsed) => parsed.timestamp(),
                    Err(_) => {
                        tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_TIME_INVALID");
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
                let valid_until = if event.valid_until.is_empty() {
                    None
                } else {
                    let parsed = match chrono::DateTime::parse_from_rfc3339(&event.valid_until) {
                        Ok(parsed) => parsed,
                        Err(_) => {
                            tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_TIME_INVALID");
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
                    if parsed.timestamp() <= effective_at {
                        tracing::warn!(event_code = "ZONE_STORAGE_ADMISSION_WINDOW_INVALID");
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
                    Some(parsed.timestamp())
                };
                let admission = StorageAdmission {
                    resource_id: target.resource_id.clone(),
                    resource_name: target.resource_name.clone(),
                    wallet_version: event.wallet_version,
                    admission_mode: event.admission_mode.clone(),
                    restriction_reason: if event.restriction_reason.is_empty() {
                        None
                    } else {
                        Some(event.restriction_reason.clone())
                    },
                    effective_at_unix_seconds: effective_at,
                    valid_until_unix_seconds: valid_until,
                    source_event_id: event.event_id.clone(),
                };
                state
                    .update_storage_admission(&target.resource_id, admission.clone())
                    .await?;
                state
                    .update_storage_admission_name_index(&target.resource_name, admission)
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
            }
        }
    }
    Ok(())
}

async fn wait_or_cancel(shutdown: &CancellationToken, duration: Duration) -> bool {
    tokio::select! {
        _ = shutdown.cancelled() => false,
        _ = tokio::time::sleep(duration) => true,
    }
}
