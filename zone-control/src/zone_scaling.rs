use bytes::Bytes;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio_util::sync::CancellationToken;

use crate::transfer_ticket::config::Config;
use crate::zone_control_state::ZoneControlState;

const DIRECTIVE_KEY: &str = "signal.workers.scale";

#[derive(Clone, Debug, Deserialize, Serialize)]
struct WorkerScaleDirective {
    zone_id: String,
    target_per_node: usize,
    observed_zone_lag: u64,
    lag_stale: bool,
    issued_at_unix_ms: u64,
    expires_at_unix_ms: u64,
    assignment_epoch: u64,
}

#[derive(Deserialize)]
struct NodeSnapshot {
    cpu: f64,
    ram: f64,
    active_workers: usize,
    updated_at: u64,
    #[serde(default)]
    job_queue_lag: u64,
    #[serde(default = "default_true")]
    job_queue_lag_stale: bool,
}

pub(crate) async fn run_worker_scale_controller(
    config: Config,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    let mut last_target = read_last_target(&state, &config).await;
    loop {
        let now_ms = unix_time_millis();
        let mut alive_nodes = 0_usize;
        let mut active_workers = 0_usize;
        let mut queue_lag = 0_u64;
        let mut lag_stale = false;
        let mut max_cpu = 0.0_f64;
        let mut max_ram = 0.0_f64;
        for key in state.health_keys().await? {
            if !key.starts_with("zone.node.") {
                continue;
            }
            let Some(value) = state.health_get(&key).await? else {
                continue;
            };
            let Ok(node) = serde_json::from_slice::<NodeSnapshot>(&value) else {
                continue;
            };
            if now_ms.saturating_sub(node.updated_at.saturating_mul(1_000)) > 15_000 {
                continue;
            }
            if !node.cpu.is_finite()
                || !(0.0..=1.0).contains(&node.cpu)
                || !node.ram.is_finite()
                || !(0.0..=1.0).contains(&node.ram)
            {
                lag_stale = true;
                continue;
            }
            alive_nodes += 1;
            active_workers = active_workers.saturating_add(node.active_workers);
            queue_lag = queue_lag.saturating_add(node.job_queue_lag);
            lag_stale |= node.job_queue_lag_stale;
            max_cpu = max_cpu.max(node.cpu);
            max_ram = max_ram.max(node.ram);
        }
        if alive_nodes == 0 {
            lag_stale = true;
        }
        if !lag_stale {
            let current_per_node =
                active_workers.saturating_add(alive_nodes.saturating_sub(1)) / alive_nodes.max(1);
            let mut next_target = current_per_node.clamp(config.min_workers, config.max_workers);
            if queue_lag > (alive_nodes as u64).saturating_mul(100)
                && max_cpu < 0.9
                && max_ram < 0.9
            {
                next_target = next_target.saturating_add(1).min(config.max_workers);
            } else if queue_lag == 0 && max_cpu > 0.9 && max_ram > 0.9 {
                next_target = next_target.saturating_sub(1).max(config.min_workers);
            }
            last_target = next_target;
        }
        let directive = WorkerScaleDirective {
            zone_id: config.zone_id.clone(),
            target_per_node: last_target,
            observed_zone_lag: queue_lag,
            lag_stale,
            issued_at_unix_ms: now_ms,
            expires_at_unix_ms: now_ms.saturating_add(15_000),
            assignment_epoch,
        };
        state
            .coordination_put_fenced(
                DIRECTIVE_KEY,
                Bytes::from(serde_json::to_vec(&directive).map_err(|error| error.to_string())?),
                assignment_epoch,
            )
            .await?;
        if !wait_or_cancel(&shutdown, Duration::from_secs(5)).await {
            return Ok(());
        }
    }
}

async fn read_last_target(state: &ZoneControlState, config: &Config) -> usize {
    state
        .coordination_get(DIRECTIVE_KEY)
        .await
        .ok()
        .flatten()
        .and_then(|value| serde_json::from_slice::<WorkerScaleDirective>(&value).ok())
        .filter(|directive| directive.zone_id == config.zone_id)
        .map(|directive| {
            directive
                .target_per_node
                .clamp(config.min_workers, config.max_workers)
        })
        .unwrap_or(config.min_workers)
}

fn default_true() -> bool {
    true
}
fn unix_time_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|value| value.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}
async fn wait_or_cancel(shutdown: &CancellationToken, duration: Duration) -> bool {
    tokio::select! { _ = shutdown.cancelled() => false, _ = tokio::time::sleep(duration) => true }
}
