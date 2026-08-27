use std::sync::Arc;
use std::time::Duration;

use prost::Message;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::storage_admission_proto::StorageAdmissionChangedV1;
use crate::transfer_ticket::config::Config;
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
    let topic = kafka.storage_commercial_admission_topic(&config.zone_id);
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
                let event = match StorageAdmissionChangedV1::decode(value.as_ref()) {
                    Ok(event)
                        if !event.event_id.is_empty()
                            && event.policy_version > 0
                            && (event.decision == "ALLOW"
                                || event.decision == "SUSPEND_BILLABLE")
                            && ((event.decision == "ALLOW"
                                && event.restriction_reason.is_empty())
                                || (event.decision == "SUSPEND_BILLABLE"
                                    && !event.restriction_reason.is_empty()))
                            && (event.owner_type == "PERSONAL" || event.owner_type == "TENANT")
                            && !event.resource_id.is_empty()
                            && !event.resource_name.is_empty()
                            && !event.zone_id.is_empty() =>
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
                let resource_id = Uuid::parse_str(&event.resource_id).ok();
                if event.zone_id != expected_zone.to_string()
                    || resource_id.is_none_or(|value| value.is_nil())
                    || event.resource_name.len() > 255
                    || (!event.resource_name.starts_with("ws-")
                        && !event.resource_name.starts_with("tn-"))
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
                    resource_id: event.resource_id.clone(),
                    resource_name: event.resource_name.clone(),
                    policy_version: event.policy_version,
                    decision: event.decision.clone(),
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
                    .update_storage_admission(&event.resource_id, admission.clone())
                    .await?;
                state
                    .update_storage_admission_name_index(&event.resource_name, admission)
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn storage_owned_admission_proto_round_trips() {
        let event = StorageAdmissionChangedV1 {
            event_id: Uuid::new_v4().to_string(),
            owner_id: Uuid::new_v4().to_string(),
            owner_type: "PERSONAL".to_string(),
            policy_version: 7,
            decision: "ALLOW".to_string(),
            restriction_reason: String::new(),
            effective_at: "2026-08-16T10:00:00Z".to_string(),
            valid_until: String::new(),
            resource_id: Uuid::new_v4().to_string(),
            resource_name: "ws-contract-bucket".to_string(),
            zone_id: Uuid::new_v4().to_string(),
        };
        let decoded = StorageAdmissionChangedV1::decode(event.encode_to_vec().as_slice()).unwrap();
        assert_eq!(decoded, event);
        assert_eq!(decoded.policy_version, 7);
        assert_eq!(decoded.decision, "ALLOW");
    }
}
