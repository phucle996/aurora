use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::observability::logger::{LogFields, Logger};
use crate::workerpool::pool::{TaskGuard, WorkerLifecycleManager};
use crate::workerpool::runtime::WorkerJobRuntime;

pub(crate) const WORKER_SCALE_DIRECTIVE_KEY: &str = "signal.workers.scale";

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct WorkerScaleDirective {
    pub zone_id: String,
    pub target_per_node: usize,
    pub observed_zone_lag: u64,
    pub lag_stale: bool,
    pub issued_at_unix_ms: u64,
    pub expires_at_unix_ms: u64,
    pub assignment_epoch: u64,
}

fn validate_worker_scale_directive_target(
    directive: &WorkerScaleDirective,
    zone_id: &str,
    min_workers: usize,
    max_workers: usize,
    now_ms: u64,
    last_assignment_epoch: u64,
    last_issued_at_unix_ms: u64,
) -> Option<usize> {
    (directive.zone_id == zone_id
        && !directive.lag_stale
        && directive.assignment_epoch > 0
        && directive.issued_at_unix_ms <= directive.expires_at_unix_ms
        && directive.expires_at_unix_ms > now_ms
        && directive.target_per_node >= min_workers
        && directive.target_per_node <= max_workers
        && (directive.assignment_epoch > last_assignment_epoch
            || (directive.assignment_epoch == last_assignment_epoch
                && directive.issued_at_unix_ms >= last_issued_at_unix_ms)))
        .then_some(directive.target_per_node)
}

pub(crate) fn start_worker_scale_directive_follower(
    worker_pool: Arc<WorkerLifecycleManager>,
    runtime: Arc<WorkerJobRuntime>,
    task_guard: TaskGuard,
) {
    tokio::spawn(async move {
        let _task_guard = task_guard;
        let shutdown = worker_pool.cancel_token();
        let mut last_failure_code: Option<&'static str> = None;
        let mut last_assignment_epoch = 0_u64;
        let mut last_issued_at_unix_ms = 0_u64;
        loop {
            let directive = match runtime
                .zone_kv()
                .coordination_get(WORKER_SCALE_DIRECTIVE_KEY)
                .await
            {
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
                            &runtime.config().zone_id,
                            runtime.config().min_workers,
                            runtime.config().max_workers,
                            current_unix_time_millis(),
                            last_assignment_epoch,
                            last_issued_at_unix_ms,
                        )
                        .is_none()
                        {
                            log_scale_follower_failure_transition(
                                &mut last_failure_code,
                                "WORKER_SCALE_DIRECTIVE_CONTRACT_INVALID",
                                "Ignored stale-lag, wrong-zone, expired, out-of-order, unfenced, or out-of-bounds worker scale directive",
                                "",
                                LogFields {
                                    fencing_token: Some(directive.assignment_epoch),
                                    outcome: Some("rejected"),
                                    ..LogFields::default()
                                },
                            );
                            None
                        } else {
                            last_assignment_epoch = directive.assignment_epoch;
                            last_issued_at_unix_ms = directive.issued_at_unix_ms;
                            log_scale_follower_recovered(&mut last_failure_code);
                            Some(directive)
                        }
                    }
                },
            };
            if let Some(directive) = directive {
                apply_worker_scale_directive_target(
                    directive.target_per_node,
                    &worker_pool,
                    &runtime,
                )
                .await;
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

async fn apply_worker_scale_directive_target(
    target: usize,
    worker_pool: &Arc<WorkerLifecycleManager>,
    runtime: &Arc<WorkerJobRuntime>,
) {
    let active_ids = worker_pool.active_worker_ids();
    let current = active_ids.len();
    crate::observability::metrics::WorkerControlMetrics::record_scale_target(
        &runtime.config().zone_id,
        "follower",
        target,
    );
    if target == current {
        return;
    }
    Logger::sys_info(
        "worker.scale_follower",
        &format!("Applying assigned Zone Control scale directive: {current} -> {target} workers"),
    );
    if target > current {
        for worker_id in 1..=target {
            if !active_ids.contains(&worker_id) {
                worker_pool.spawn_worker(worker_id, runtime.clone());
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
            assignment_epoch: 7,
        }
    }

    #[test]
    fn accepts_only_current_zone_unexpired_fenced_target() {
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-a", 1, 10, 150, 0, 0),
            Some(3)
        );
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-a", 1, 10, 150, 7, 99,),
            Some(3)
        );
    }

    #[test]
    fn rejects_expired_or_wrong_zone_target() {
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-a", 1, 10, 200, 0, 0),
            None
        );
        assert_eq!(
            validate_worker_scale_directive_target(&directive(), "zone-b", 1, 10, 150, 0, 0),
            None
        );
    }

    #[test]
    fn rejects_target_outside_local_bounds_or_without_fence() {
        let mut value = directive();
        value.target_per_node = 11;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150, 0, 0),
            None
        );
        value.target_per_node = 3;
        value.assignment_epoch = 0;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150, 0, 0),
            None
        );
    }

    #[test]
    fn rejects_stale_lag_and_out_of_order_directive() {
        let mut value = directive();
        value.lag_stale = true;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150, 0, 0),
            None
        );

        value.lag_stale = false;
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150, 8, 0),
            None
        );
        assert_eq!(
            validate_worker_scale_directive_target(&value, "zone-a", 1, 10, 150, 7, 101),
            None
        );
    }
}
