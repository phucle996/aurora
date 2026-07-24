use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use bytes::Bytes;
use futures_util::{stream, StreamExt};

use super::scale_policy::{WorkerScalePolicy, WorkerScaleSignals};
use super::session::ZoneLeaderSession;
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
