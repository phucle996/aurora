use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use bytes::Bytes;

use super::{LocalMailNodeSnapshot, MailRuntime};
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};
use tokio_util::sync::CancellationToken;

/// [COMMENT]: Local observer tuyệt đối không gọi JMAP/Stalwart. Nó chỉ export
/// state của pod để assigned Zone Control workers aggregate; vì vậy scale-out
/// không nhân số request health tới hạ tầng.
pub(super) fn start_mail_dataplane_local_snapshot_writer(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    runtime: Arc<MailRuntime>,
    shutdown: CancellationToken,
) {
    tokio::spawn(async move {
        loop {
            if shutdown.is_cancelled() {
                return;
            }
            let active_slots = runtime
                .runtime_snapshots()
                .iter()
                .filter(|snapshot| {
                    matches!(
                        snapshot.state.as_str(),
                        "STARTING" | "RUNNING" | "PAUSED" | "DRAINING" | "DEGRADED"
                    )
                })
                .count()
                .min(u64::MAX as usize) as u64;
            runtime.metrics.record_local_runtime_slots(active_slots);

            let observed_at_unix_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let snapshot = LocalMailNodeSnapshot {
                node_id: runtime.runtime_node_id.clone(),
                boot_id: runtime.runtime_boot_id.to_string(),
                pending_items: runtime.metrics.pending_items.load(Ordering::Relaxed),
                in_flight_batches: runtime.metrics.in_flight_batches.load(Ordering::Relaxed),
                queue_capacity: config.mail_batch_queue_capacity,
                observed_at_unix_ms,
            };
            match serde_json::to_vec(&snapshot) {
                Ok(value) => {
                    if let Err(error) = zone_kv
                        .health_put(
                            format!("mail.health.node.{}", runtime.runtime_node_id),
                            Bytes::from(value),
                        )
                        .await
                    {
                        Logger::sys_warn_with_fields(
                            "mail.local_snapshot",
                            "MAIL_LOCAL_SNAPSHOT_WRITE_FAILED",
                            "Could not write pod-local mail runtime snapshot to Zone health KV",
                            &error,
                            LogFields {
                                operation_id: Some(&runtime.runtime_node_id),
                                retryable: Some(true),
                                outcome: Some("stale"),
                                ..LogFields::default()
                            },
                        );
                    }
                }
                Err(error) => Logger::sys_error(
                    "mail.local_snapshot",
                    "Could not serialize pod-local mail runtime snapshot",
                    &error.to_string(),
                ),
            }

            tokio::select! {
                _ = shutdown.cancelled() => return,
                _ = tokio::time::sleep(Duration::from_millis(
                    config.mail_health_observe_interval_ms.max(1_000),
                )) => {}
            }
        }
    });
}
