use std::sync::atomic::AtomicUsize;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use crate::workerpool::watchdog::ActiveLockRegistry;

pub(crate) const WORKER_SCALE_DIRECTIVE_KEY: &str = "signal.workers.scale";

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct WorkerScaleDirective {
    pub zone_id: String,
    pub target_per_node: usize,
    pub observed_zone_lag: u64,
    pub lag_stale: bool,
    pub issued_at_unix_ms: u64,
    pub expires_at_unix_ms: u64,
    pub leader_fencing_token: u64,
}

fn validate_worker_scale_directive_target(
    directive: &WorkerScaleDirective,
    zone_id: &str,
    min_workers: usize,
    max_workers: usize,
    now_ms: u64,
) -> Option<usize> {
    (directive.zone_id == zone_id
        && directive.leader_fencing_token > 0
        && directive.expires_at_unix_ms > now_ms
        && directive.target_per_node >= min_workers
        && directive.target_per_node <= max_workers)
        .then_some(directive.target_per_node)
}

#[allow(clippy::too_many_arguments)]
pub(crate) fn start_worker_scale_follower(
    config: Arc<Config>,
    worker_pool: Arc<WorkerLifecycleManager>,
    kafka: Arc<KafkaTransport>,
    zone_kv: Arc<ZoneKvStore>,
    active_lock_registry: Arc<ActiveLockRegistry>,
    rx: Arc<
        tokio::sync::Mutex<tokio::sync::mpsc::Receiver<crate::job_lifecycle::message::JobPayload>>,
    >,
    active_jobs: Arc<AtomicUsize>,
) {
    tokio::spawn(async move {
        let shutdown = worker_pool.cancel_token();
        let mut last_failure_code: Option<&'static str> = None;
        loop {
            let directive = match zone_kv.coordination_get(WORKER_SCALE_DIRECTIVE_KEY).await {
                Err(error) => {
                    log_scale_follower_failure_transition(
                        &mut last_failure_code,
                        "WORKER_SCALE_DIRECTIVE_READ_FAILED",
                        "Could not read fenced worker scale directive; retaining current capacity",
                        &error,
                        LogFields {
                            retryable: Some(true),
                            outcome: Some("capacity_held"),
                            ..LogFields::default()
                        },
                    );
                    None
                }
                Ok(None) => {
                    log_scale_follower_recovered(&mut last_failure_code);
                    None
                }
                Ok(Some(bytes)) => match serde_json::from_slice::<WorkerScaleDirective>(&bytes) {
                    Err(error) => {
                        log_scale_follower_failure_transition(
                            &mut last_failure_code,
                            "WORKER_SCALE_DIRECTIVE_INVALID_JSON",
                            "Ignored malformed worker scale directive; retaining current capacity",
                            &error.to_string(),
                            LogFields {
                                outcome: Some("rejected"),
                                ..LogFields::default()
                            },
                        );
                        None
                    }
                    Ok(directive) => {
                        if validate_worker_scale_directive_target(
                            &directive,
                            &config.zone_id,
                            config.min_workers,
                            config.max_workers,
                            current_unix_time_millis(),
                        )
                        .is_none()
                        {
                            log_scale_follower_failure_transition(
                                &mut last_failure_code,
                                "WORKER_SCALE_DIRECTIVE_CONTRACT_INVALID",
                                "Ignored wrong-zone, expired, unfenced, or out-of-bounds worker scale directive",
                                "",
                                LogFields {
                                    leader_fencing_token: Some(
                                        directive.leader_fencing_token,
                                    ),
                                    outcome: Some("rejected"),
                                    ..LogFields::default()
                                },
                            );
                            None
                        } else {
                            log_scale_follower_recovered(&mut last_failure_code);
                            Some(directive)
                        }
                    }
                },
            };
            if let Some(directive) = directive {
                apply_worker_scale_directive_target(
                    directive.target_per_node,
                    &config,
                    &worker_pool,
                    &kafka,
                    &zone_kv,
                    &active_lock_registry,
                    &rx,
                    &active_jobs,
                )
                .await;
                crate::workerpool::metrics::WorkerMetricsManager::record_metrics(
                    crate::workerpool::metrics::MetricsType::KafkaConsumerLag {
                        zone_id: config.zone_id.clone(),
                        lag: directive.observed_zone_lag,
                    },
                );
                crate::workerpool::metrics::WorkerMetricsManager::record_metrics(
                    crate::workerpool::metrics::MetricsType::ActiveConnectionsCount {
                        zone_id: config.zone_id.clone(),
                        count: worker_pool.active_worker_ids().len(),
                    },
                );
            }

            tokio::select! {
                _ = shutdown.cancelled() => return,
                _ = tokio::time::sleep(Duration::from_secs(2)) => {}
            }
        }
    });
}

fn log_scale_follower_failure_transition(
    last_failure_code: &mut Option<&'static str>,
    event_code: &'static str,
    message: &str,
    error: &str,
    fields: LogFields<'_>,
) {
    if *last_failure_code == Some(event_code) {
        return;
    }
    *last_failure_code = Some(event_code);
    Logger::sys_warn_with_fields("worker.scale_follower", event_code, message, error, fields);
}

fn log_scale_follower_recovered(last_failure_code: &mut Option<&'static str>) {
    if last_failure_code.take().is_some() {
        Logger::sys_info_with_fields(
            "worker.scale_follower",
            "WORKER_SCALE_DIRECTIVE_READ_RECOVERED",
            "Worker scale follower recovered directive visibility",
            LogFields {
                outcome: Some("recovered"),
                ..LogFields::default()
            },
        );
    }
}

#[allow(clippy::too_many_arguments)]
async fn apply_worker_scale_directive_target(
    target: usize,
    config: &Arc<Config>,
    worker_pool: &Arc<WorkerLifecycleManager>,
    kafka: &Arc<KafkaTransport>,
    zone_kv: &Arc<ZoneKvStore>,
    active_lock_registry: &Arc<ActiveLockRegistry>,
    rx: &Arc<
        tokio::sync::Mutex<tokio::sync::mpsc::Receiver<crate::job_lifecycle::message::JobPayload>>,
    >,
    active_jobs: &Arc<AtomicUsize>,
) {
    let active_ids = worker_pool.active_worker_ids();
    let current = active_ids.len();
    if target == current {
        return;
    }
    Logger::sys_info(
        "worker.scale_follower",
        &format!("Áp dụng leader scale directive: {current} -> {target} workers"),
    );
    if target > current {
        for worker_id in 1..=target {
            if !active_ids.contains(&worker_id) {
                worker_pool
                    .spawn_worker(
                        worker_id,
                        config.clone(),
                        kafka.clone(),
                        zone_kv.clone(),
                        active_lock_registry.clone(),
                        rx.clone(),
                        active_jobs.clone(),
                    )
                    .await;
            }
        }
    } else {
        let mut sorted_ids = active_ids;
        sorted_ids.sort_unstable_by(|left, right| right.cmp(left));
        for worker_id in sorted_ids.into_iter().take(current - target) {
            worker_pool.terminate_worker(worker_id);
        }
    }
}

fn current_unix_time_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::{validate_worker_scale_directive_target, WorkerScaleDirective};

    fn directive() -> WorkerScaleDirective {
        WorkerScaleDirective {
            zone_id: "zone-a".to_string(),
            target_per_node: 3,
            observed_zone_lag: 10,
            lag_stale: false,
            issued_at_unix_ms: 100,
            expires_at_unix_ms: 200,
            leader_fencing_token: 7,
        }
    }

    #[test]
    fn accepts_only_current_zone_unexpired_fenced_target() {
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-a", 1, 10, 150),
            Some(3)
        );
    }

    #[test]
    fn rejects_expired_or_wrong_zone_target() {
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-a", 1, 10, 200),
            None
        );
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-b", 1, 10, 150),
            None
        );
    }

    #[test]
    fn rejects_target_outside_local_bounds_or_without_fence() {
        let mut value = directive();
        value.target_per_node = 11;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150),
            None
        );
        value.target_per_node = 3;
        value.leader_fencing_token = 0;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150),
            None
        );
    }
}
