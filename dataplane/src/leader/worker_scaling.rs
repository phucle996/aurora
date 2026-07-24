use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use bytes::Bytes;
use futures_util::{stream, StreamExt};

use super::leadership::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};
use crate::observability::metrics::{NodeRuntimeSample, NODE_RUNTIME_SAMPLE_MAX_AGE_MS};
use crate::workerpool::scale_follower::{WorkerScaleDirective, WORKER_SCALE_DIRECTIVE_KEY};

const NODE_RUNTIME_KEY_RETENTION_SECONDS: u64 = 300;

pub(crate) async fn run_zone_worker_scale_controller(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
) {
    let mut scale_policy = WorkerScalePolicy::new(config.min_workers, config.max_workers);
    let mut last_target = config.min_workers.min(config.max_workers);

    loop {
        let now_ms = current_unix_time_millis();
        let now_seconds = now_ms / 1_000;
        let mut alive_nodes = 0_usize;
        let mut active_workers = 0_usize;
        let mut zone_lag = 0_u64;
        let mut lag_stale = false;
        let mut runtime_stale = false;
        let mut max_cpu = 0.0_f64;
        let mut max_memory = 0.0_f64;
        let mut max_cpu_throttled = 0.0_f64;
        let health_keys = match zone_kv.health_keys().await {
            Ok(keys) => keys,
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.zone_worker_scale_controller",
                    "WORKER_SCALE_HEALTH_KEYS_READ_FAILED",
                    "Cannot enumerate node lag snapshots; retaining the last fenced scale target",
                    &error,
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        retryable: Some(true),
                        outcome: Some("target_held"),
                        ..LogFields::default()
                    },
                );
                if !session.wait(Duration::from_secs(1)).await {
                    return;
                }
                continue;
            }
        };
        let node_snapshots = stream::iter(
            health_keys
                .into_iter()
                .filter(|key| key.starts_with("zone.node.")),
        )
        .map(|key| {
            let zone_kv = zone_kv.clone();
            async move {
                let value = zone_kv.health_get(&key).await;
                (key, value)
            }
        })
        .buffer_unordered(32)
        .collect::<Vec<_>>()
        .await;
        let mut stale_node_keys = Vec::new();
        for (key, value) in node_snapshots {
            let value = match value {
                Ok(Some(value)) => value,
                Ok(None) => continue,
                Err(error) => {
                    Logger::sys_warn(
                        "leader.zone_worker_scale_controller",
                        &format!("Could not read node lag snapshot key={key}"),
                        &error,
                    );
                    continue;
                }
            };
            let Ok(node) = serde_json::from_slice::<NodeRuntimeSample>(&value) else {
                Logger::sys_warn(
                    "leader.zone_worker_scale_controller",
                    &format!("Ignored malformed node lag snapshot key={key}"),
                    "WORKER_SCALE_NODE_SNAPSHOT_INVALID",
                );
                stale_node_keys.push(key);
                continue;
            };
            if node.updated_at == 0 {
                stale_node_keys.push(key);
                continue;
            }
            let sample_age_seconds = now_seconds.saturating_sub(node.updated_at);
            if sample_age_seconds > NODE_RUNTIME_KEY_RETENTION_SECONDS {
                stale_node_keys.push(key);
                continue;
            }
            if sample_age_seconds > 15 {
                continue;
            }
            // A fresh but invalid sample is different from an old pod key:
            // hold the fenced target until that node can prove its current
            // resource state again.
            let numeric_sample_valid = node.cpu.is_finite()
                && node.cpu.clamp(0.0, 1.0) == node.cpu
                && node.ram.is_finite()
                && node.ram.clamp(0.0, 1.0) == node.ram
                && node.cpu_throttled_ratio.is_finite()
                && node.cpu_throttled_ratio.clamp(0.0, 1.0) == node.cpu_throttled_ratio;
            let sample_fresh = node.sample_valid
                && numeric_sample_valid
                && node.sample_observed_at_unix_ms > 0
                && now_ms
                    .checked_sub(node.sample_observed_at_unix_ms)
                    .is_some_and(|age| age <= NODE_RUNTIME_SAMPLE_MAX_AGE_MS);
            let lag_fresh = !node.job_queue_lag_stale
                && node.job_queue_lag_observed_at_unix_ms > 0
                && now_ms
                    .checked_sub(node.job_queue_lag_observed_at_unix_ms)
                    .is_some_and(|age| age <= NODE_RUNTIME_SAMPLE_MAX_AGE_MS);
            if !sample_fresh || !lag_fresh {
                runtime_stale = true;
                continue;
            }
            alive_nodes += 1;
            active_workers = active_workers.saturating_add(node.active_workers);
            zone_lag = zone_lag.saturating_add(node.job_queue_lag);
            lag_stale |= node.job_queue_lag_stale;
            max_cpu = max_cpu.max(node.cpu);
            max_memory = max_memory.max(node.ram);
            max_cpu_throttled = max_cpu_throttled.max(node.cpu_throttled_ratio);
        }
        if !stale_node_keys.is_empty() && session.permits_external_side_effect().await {
            stream::iter(stale_node_keys)
                .for_each_concurrent(16, |key| {
                    let zone_kv = zone_kv.clone();
                    async move {
                        if let Err(error) = zone_kv.health_delete(&key).await {
                            Logger::sys_warn(
                                "leader.zone_worker_scale_controller",
                                &format!("Could not delete stale node runtime snapshot key={key}"),
                                &error,
                            );
                        }
                    }
                })
                .await;
        }
        if alive_nodes == 0 {
            lag_stale = true;
            runtime_stale = true;
        }

        // [COMMENT]: Stale/missing lag or runtime telemetry must not scale
        // blindly; the last fenced target is the safe recovery point.
        if !lag_stale && !runtime_stale {
            let current_per_node =
                active_workers.saturating_add(alive_nodes.saturating_sub(1)) / alive_nodes.max(1);
            let next_target = scale_policy.evaluate(
                current_per_node,
                WorkerScaleSignals {
                    queue_lag: zone_lag,
                    cpu_utilization: max_cpu,
                    memory_utilization: max_memory,
                    cpu_throttled_ratio: max_cpu_throttled,
                },
                now_ms,
            );
            if next_target != last_target {
                Logger::sys_info_with_fields(
                    "leader.zone_worker_scale_controller",
                    "WORKER_SCALE_TARGET_CHANGED",
                    &format!(
                        "Leader changed per-node worker target from {last_target} to {next_target}"
                    ),
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        outcome: Some("directive_updated"),
                        ..LogFields::default()
                    },
                );
            }
            last_target = next_target;
        } else {
            scale_policy.hold_on_stale_observation();
        }
        let directive = WorkerScaleDirective {
            zone_id: config.zone_id.clone(),
            target_per_node: last_target,
            observed_zone_lag: zone_lag,
            lag_stale: lag_stale || runtime_stale,
            issued_at_unix_ms: now_ms,
            expires_at_unix_ms: now_ms.saturating_add(15_000),
            leader_fencing_token: session.fencing_token(),
        };
        if session.permits_external_side_effect().await {
            match serde_json::to_vec(&directive) {
                Ok(value) => {
                    if let Err(error) = zone_kv
                        .coordination_put_fenced(
                            WORKER_SCALE_DIRECTIVE_KEY,
                            Bytes::from(value),
                            session.fencing_token(),
                        )
                        .await
                    {
                        Logger::sys_warn(
                            "leader.zone_worker_scale_controller",
                            "Không ghi được worker scale directive",
                            &error,
                        );
                    } else {
                        crate::observability::metrics::WorkerControlMetrics::record_scale_target(
                            &config.zone_id,
                            "leader",
                            last_target,
                        );
                    }
                }
                Err(error) => Logger::sys_warn(
                    "leader.zone_worker_scale_controller",
                    "Không encode được worker scale directive",
                    &error.to_string(),
                ),
            }
        }

        if !session.wait(Duration::from_secs(5)).await {
            return;
        }
    }
}

fn current_unix_time_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}

/// Leader-owned scale policy for worker capacity.
///
/// Consecutive observations and cooldowns prevent every five-second telemetry
/// fluctuation from turning into worker churn across the whole Zone.
pub struct WorkerScalePolicy {
    min_workers: usize,
    max_workers: usize,
    scale_up_streak: u8,
    scale_down_streak: u8,
    last_change_at_unix_ms: Option<u64>,
}

#[derive(Clone, Copy, Debug, Default)]
pub struct WorkerScaleSignals {
    pub queue_lag: u64,
    pub cpu_utilization: f64,
    pub memory_utilization: f64,
    pub cpu_throttled_ratio: f64,
}

impl WorkerScalePolicy {
    pub fn new(min_workers: usize, max_workers: usize) -> Self {
        Self {
            min_workers: min_workers.min(max_workers),
            max_workers,
            scale_up_streak: 0,
            scale_down_streak: 0,
            last_change_at_unix_ms: None,
        }
    }

    pub fn evaluate(
        &mut self,
        current_workers: usize,
        signals: WorkerScaleSignals,
        now_ms: u64,
    ) -> usize {
        let current_workers = current_workers.min(self.max_workers);
        if current_workers < self.min_workers {
            self.record_change(now_ms);
            return self.min_workers;
        }

        let cpu_pressure = signals.cpu_utilization >= 0.95;
        let memory_pressure = signals.memory_utilization >= 0.90;
        let throttled = signals.cpu_throttled_ratio >= 0.20;
        if cpu_pressure || memory_pressure || throttled {
            self.reset_streaks();
            return current_workers;
        }

        let has_scale_up_headroom = signals.queue_lag > 100
            && signals.cpu_utilization < 0.80
            && signals.memory_utilization < 0.80
            && signals.cpu_throttled_ratio < 0.10;
        if has_scale_up_headroom {
            self.scale_down_streak = 0;
            self.scale_up_streak = self.scale_up_streak.saturating_add(1);
            if self.scale_up_streak >= 2 && self.cooldown_elapsed(now_ms, 15_000) {
                self.scale_up_streak = 0;
                let target = current_workers.saturating_add(2).min(self.max_workers);
                if target != current_workers {
                    self.record_change(now_ms);
                }
                return target;
            }
            return current_workers;
        }

        let is_calm = signals.queue_lag == 0
            && signals.cpu_utilization < 0.60
            && signals.memory_utilization < 0.65;
        if is_calm {
            self.scale_up_streak = 0;
            self.scale_down_streak = self.scale_down_streak.saturating_add(1);
            if self.scale_down_streak >= 6 && self.cooldown_elapsed(now_ms, 30_000) {
                self.scale_down_streak = 0;
                let target = current_workers.saturating_sub(1).max(self.min_workers);
                if target != current_workers {
                    self.record_change(now_ms);
                }
                return target;
            }
            return current_workers;
        }

        self.reset_streaks();
        current_workers
    }

    /// Drop confirmation streaks when telemetry is stale; a fresh window must
    /// prove the condition again instead of inheriting pre-outage samples.
    pub fn hold_on_stale_observation(&mut self) {
        self.reset_streaks();
    }

    fn cooldown_elapsed(&self, now_ms: u64, cooldown_ms: u64) -> bool {
        self.last_change_at_unix_ms
            .is_none_or(|last_change| now_ms.saturating_sub(last_change) >= cooldown_ms)
    }

    fn record_change(&mut self, now_ms: u64) {
        self.last_change_at_unix_ms = Some(now_ms);
    }

    fn reset_streaks(&mut self) {
        self.scale_up_streak = 0;
        self.scale_down_streak = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::{WorkerScalePolicy, WorkerScaleSignals};

    fn signals(queue_lag: u64, cpu: f64, memory: f64, throttled: f64) -> WorkerScaleSignals {
        WorkerScaleSignals {
            queue_lag,
            cpu_utilization: cpu,
            memory_utilization: memory,
            cpu_throttled_ratio: throttled,
        }
    }

    #[test]
    fn resource_pressure_freezes_scale_up() {
        let mut policy = WorkerScalePolicy::new(1, 10);
        assert_eq!(policy.evaluate(3, signals(500, 0.96, 0.2, 0.0), 0), 3);
        assert_eq!(policy.evaluate(3, signals(500, 0.2, 0.91, 0.0), 5_000), 3);
        assert_eq!(policy.evaluate(3, signals(500, 0.2, 0.2, 0.21), 10_000), 3);
    }

    #[test]
    fn lag_requires_two_observations_and_respects_cooldown() {
        let mut policy = WorkerScalePolicy::new(1, 10);
        assert_eq!(policy.evaluate(3, signals(101, 0.5, 0.5, 0.01), 0), 3);
        assert_eq!(policy.evaluate(3, signals(101, 0.5, 0.5, 0.01), 5_000), 5);
        assert_eq!(policy.evaluate(5, signals(101, 0.5, 0.5, 0.01), 10_000), 5);
        assert_eq!(policy.evaluate(5, signals(101, 0.5, 0.5, 0.01), 20_000), 7);
    }

    #[test]
    fn empty_queue_scales_down_one_slot_after_six_calm_samples() {
        let mut policy = WorkerScalePolicy::new(2, 10);
        for sample in 0..5 {
            assert_eq!(
                policy.evaluate(8, signals(0, 0.2, 0.3, 0.0), sample * 5_000),
                8
            );
        }
        assert_eq!(policy.evaluate(8, signals(0, 0.2, 0.3, 0.0), 25_000), 7);
        assert_eq!(policy.evaluate(7, signals(0, 0.7, 0.3, 0.0), 30_000), 7);
    }
}
