use super::super::MailRuntime;
use crate::config::Config;
use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeReportedV1, MailConsumerRuntimeState,
    MailEventMetadataV1,
};
use crate::infra::nats_core::NatsCoreTransport;
use crate::observability::logger::Logger;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use uuid::Uuid;

/// [COMMENT]: Reporter chỉ đọc pod-local memory và watch lease đã nhận qua NATS Core.
/// Dataplane không đọc/ghi Shared L2 Redis; mất report là soft-state và heartbeat sau tự phục hồi.
pub(super) fn start(
    config: Arc<Config>,
    nats_core: Arc<NatsCoreTransport>,
    runtime: Arc<MailRuntime>,
) {
    tokio::spawn(async move {
        let event_namespace = Uuid::parse_str("e7b1a4a4-9150-4494-88d4-5994d312d219")
            .expect("mail consumer report namespace must be valid");
        let zone_id = match Uuid::parse_str(&config.zone_id) {
            Ok(zone_id) => zone_id,
            Err(error) => {
                Logger::sys_error(
                    "mail.supervisor.consumer_reporter",
                    "ZONE_ID is not a UUID",
                    &error.to_string(),
                );
                return;
            }
        };

        loop {
            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(i64::MAX as u128) as i64)
                .unwrap_or_default();
            let mut reports = Vec::new();

            for snapshot in runtime.runtime_snapshots() {
                let Some(runtime_epoch) = nats_core
                    .active_watch(&snapshot.consumer_id, snapshot.config_version, now_ms)
                    .await
                else {
                    continue;
                };
                let contract_valid = snapshot.report_sequence > 0
                    && snapshot.runtime_generation > 0
                    && snapshot.runtime_generation == snapshot.fencing_token
                    && snapshot.config_version > 0
                    && snapshot.heartbeat_unix_ms <= i64::MAX as u64
                    && snapshot.instance_id == format!("slot:{}", snapshot.slot)
                    && !snapshot.runtime_node_id.is_empty()
                    && snapshot.runtime_node_id.len() <= 255
                    && !snapshot.runtime_node_id.chars().any(char::is_control)
                    && now_ms.saturating_sub(snapshot.heartbeat_unix_ms as i64)
                        <= config
                            .mail_stream_slot_lease_ttl_seconds
                            .saturating_mul(2_000) as i64
                    && Uuid::parse_str(&runtime_epoch).is_ok();
                if !contract_valid {
                    continue;
                }

                let Ok(consumer_id) = Uuid::parse_str(&snapshot.consumer_id) else {
                    continue;
                };
                let Ok(runtime_boot_id) = Uuid::parse_str(&snapshot.runtime_boot_id) else {
                    continue;
                };
                let runtime_state = match snapshot.state.as_str() {
                    "STOPPED" => MailConsumerRuntimeState::Stopped,
                    "STARTING" => MailConsumerRuntimeState::Starting,
                    "RUNNING" => MailConsumerRuntimeState::Running,
                    "PAUSED" => MailConsumerRuntimeState::Paused,
                    "DRAINING" => MailConsumerRuntimeState::Draining,
                    "ERROR" => MailConsumerRuntimeState::Error,
                    "DEGRADED" => MailConsumerRuntimeState::Degraded,
                    _ => continue,
                };
                let error_code = if snapshot.error_code.len() <= 100
                    && snapshot.error_code.bytes().all(|byte| {
                        byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                    }) {
                    snapshot.error_code
                } else {
                    "MAIL_RUNTIME_ERROR_INVALID".to_string()
                };
                let event_name = format!(
                    "{}:{}:{}:{}:{}:{}",
                    zone_id,
                    consumer_id,
                    snapshot.instance_id,
                    snapshot.runtime_generation,
                    snapshot.report_sequence,
                    runtime_epoch
                );
                let event_id = Uuid::new_v5(&event_namespace, event_name.as_bytes());
                reports.push(MailConsumerRuntimeReportedV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: snapshot.heartbeat_unix_ms as i64,
                        traceparent: String::new(),
                        producer: "dataplane-mail-consumer".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: snapshot.config_version,
                    runtime_state: runtime_state as i32,
                    instance_id: snapshot.instance_id,
                    runtime_generation: snapshot.runtime_generation,
                    consumer_lag: snapshot.consumer_lag,
                    error_code,
                    // [COMMENT]: Raw broker exception không vượt reverse trust boundary.
                    error_message: String::new(),
                    report_sequence: snapshot.report_sequence,
                    runtime_epoch,
                });
                let _ = runtime_boot_id;
            }

            for chunk in reports.chunks(250) {
                let batch = MailConsumerRuntimeReportBatchV1 {
                    reports: chunk.to_vec(),
                    zone_id: zone_id.as_bytes().to_vec(),
                };
                if let Err(error) = nats_core.publish_runtime_reports(&batch).await {
                    Logger::sys_warn(
                        "mail.supervisor.consumer_reporter",
                        "NATS Core runtime report was not delivered; next heartbeat will recover",
                        &error,
                    );
                    break;
                }
            }

            tokio::time::sleep(Duration::from_millis(
                config.mail_consumer_report_interval_ms + rand::random::<u64>() % 1_000,
            ))
            .await;
        }
    });
}
