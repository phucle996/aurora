use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use bytes::Bytes;
use serde::Deserialize;

use super::zone_leader_session::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};
use crate::workerpool::auto_scale::AutoScaleEngine;
use crate::workerpool::scale_follower::{WorkerScaleDirective, WORKER_SCALE_DIRECTIVE_KEY};

#[derive(Deserialize)]
struct NodeSnapshot {
    active_workers: usize,
    updated_at: u64,
    #[serde(default)]
    job_queue_lag: u64,
    #[serde(default = "default_zone_node_lag_stale")]
    job_queue_lag_stale: bool,
}

fn default_zone_node_lag_stale() -> bool {
    true
}

pub(crate) async fn run_zone_worker_scale_controller(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
) {
    let engine = AutoScaleEngine::new(config.min_workers, config.max_workers);
    let mut last_target = config.min_workers.min(config.max_workers);

    loop {
        let now_ms = current_unix_time_millis();
        let now_seconds = now_ms / 1_000;
        let mut alive_nodes = 0_usize;
        let mut active_workers = 0_usize;
        let mut zone_lag = 0_u64;
        let mut lag_stale = false;
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
        for key in health_keys {
            if !key.starts_with("zone.node.") {
                continue;
            }
            let value = match zone_kv.health_get(&key).await {
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
            let Ok(node) = serde_json::from_slice::<NodeSnapshot>(&value) else {
                Logger::sys_warn(
                    "leader.zone_worker_scale_controller",
                    &format!("Ignored malformed node lag snapshot key={key}"),
                    "WORKER_SCALE_NODE_SNAPSHOT_INVALID",
                );
                continue;
            };
            if now_seconds.saturating_sub(node.updated_at) > 15 {
                continue;
            }
            alive_nodes += 1;
            active_workers = active_workers.saturating_add(node.active_workers);
            zone_lag = zone_lag.saturating_add(node.job_queue_lag);
            lag_stale |= node.job_queue_lag_stale;
        }
        if alive_nodes == 0 {
            lag_stale = true;
        }

        // [COMMENT]: Stale/missing lag không được scale down hoặc scale up mù; giữ quyết định cuối.
        if !lag_stale {
            let current_per_node =
                active_workers.saturating_add(alive_nodes.saturating_sub(1)) / alive_nodes.max(1);
            last_target = engine.evaluate_scale(current_per_node, zone_lag, 0.0, 0);
        }
        let directive = WorkerScaleDirective {
            zone_id: config.zone_id.clone(),
            target_per_node: last_target,
            observed_zone_lag: zone_lag,
            lag_stale,
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
