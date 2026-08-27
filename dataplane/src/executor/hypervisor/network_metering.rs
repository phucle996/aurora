use bytes::Bytes;
use prost::Message;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::sync::Arc;
use std::time::Duration;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use super::network_metering_proto::HypervisorNetworkUsageReportV1;
use super::HypervisorRuntime;
use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;

const SCHEMA_VERSION: u32 = 1;
const HOUR_MS: i64 = 3_600_000;
const REPORT_NAMESPACE: Uuid = Uuid::from_u128(0x98a4_181b_0674_5ca5_a3a1_d2ba_fbd5_1921);

#[derive(Clone, Debug, Deserialize, Serialize)]
struct PendingWindow {
    start_unix_ms: i64,
    network_in_bytes: u64,
    network_out_bytes: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct MeteringState {
    schema_version: u32,
    resource_id: String,
    last_observed_at_unix_ms: i64,
    last_network_in_bytes: u64,
    last_network_out_bytes: u64,
    pending_windows: Vec<PendingWindow>,
    #[serde(default)]
    retired_at_unix_ms: Option<i64>,
}

struct Settings {
    zone_id: Uuid,
    poll_interval: Duration,
    late_grace_ms: i64,
    state_scan_page_size: usize,
    state_scan_max_pages: usize,
}

impl Settings {
    fn from_env(config: &Config) -> Result<Self, String> {
        let zone_id = Uuid::parse_str(&config.zone_id)
            .map_err(|_| "ZONE_ID is invalid for Hypervisor network metering".to_string())?;
        if zone_id.is_nil() {
            return Err("ZONE_ID must be non-nil for Hypervisor network metering".to_string());
        }
        let poll_seconds = std::env::var("HYPERVISOR_NETWORK_METERING_POLL_SECONDS")
            .ok()
            .map(|value| value.parse::<u64>())
            .transpose()
            .map_err(|_| "HYPERVISOR_NETWORK_METERING_POLL_SECONDS is invalid".to_string())?
            .unwrap_or(60)
            .clamp(10, 300);
        let late_grace_seconds = std::env::var("HYPERVISOR_NETWORK_METERING_LATE_GRACE_SECONDS")
            .ok()
            .map(|value| value.parse::<i64>())
            .transpose()
            .map_err(|_| "HYPERVISOR_NETWORK_METERING_LATE_GRACE_SECONDS is invalid".to_string())?
            .unwrap_or(300)
            .clamp(30, 1_800);
        Ok(Self {
            zone_id,
            poll_interval: Duration::from_secs(poll_seconds),
            late_grace_ms: late_grace_seconds * 1_000,
            state_scan_page_size: std::env::var("HYPERVISOR_NETWORK_METERING_SCAN_PAGE_SIZE")
                .ok()
                .map(|value| value.parse::<usize>())
                .transpose()
                .map_err(|_| "HYPERVISOR_NETWORK_METERING_SCAN_PAGE_SIZE is invalid".to_string())?
                .unwrap_or(500)
                .clamp(50, 2_000),
            state_scan_max_pages: std::env::var("HYPERVISOR_NETWORK_METERING_SCAN_MAX_PAGES")
                .ok()
                .map(|value| value.parse::<usize>())
                .transpose()
                .map_err(|_| "HYPERVISOR_NETWORK_METERING_SCAN_MAX_PAGES is invalid".to_string())?
                .unwrap_or(20)
                .clamp(1, 200),
        })
    }
}

pub async fn run_network_metering(
    config: Arc<Config>,
    runtime: Arc<HypervisorRuntime>,
    kafka: Arc<KafkaTransport>,
    zone_kv: Arc<ZoneKvStore>,
    shutdown: CancellationToken,
) {
    let settings = match Settings::from_env(&config) {
        Ok(settings) => settings,
        Err(error) => {
            tracing::error!(event_code = "HYPERVISOR_NETWORK_METERING_CONFIG_INVALID", error = %error);
            return;
        }
    };
    let owner_id = format!(
        "{}:{}:{}",
        hostname::get()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|_| "dataplane".to_string()),
        std::process::id(),
        Uuid::new_v4()
    );
    let mut interval = tokio::time::interval(settings.poll_interval);
    let mut state_scan_skip = 0_usize;
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            _ = shutdown.cancelled() => return,
            _ = interval.tick() => {
                let lease_ttl = settings.poll_interval.saturating_mul(3).max(Duration::from_secs(60));
                match zone_kv.acquire_lease("hypervisor.network.metering.owner", &owner_id, lease_ttl).await {
                    Ok(Some(lease)) => {
                        if let Err(error) = sample_inventory(&settings, &runtime, &kafka, &zone_kv, &mut state_scan_skip).await {
                            tracing::warn!(event_code = "HYPERVISOR_NETWORK_METERING_SAMPLE_FAILED", error = %error);
                        }
                        let _ = zone_kv.release_lease(&lease).await;
                    }
                    Ok(None) => {}
                    Err(error) => tracing::warn!(event_code = "HYPERVISOR_NETWORK_METERING_LEASE_FAILED", error = %error),
                }
            }
        }
    }
}

async fn sample_inventory(
    settings: &Settings,
    runtime: &HypervisorRuntime,
    kafka: &KafkaTransport,
    zone_kv: &ZoneKvStore,
    state_scan_skip: &mut usize,
) -> Result<(), String> {
    let now_ms = chrono::Utc::now().timestamp_millis();
    let inventory = runtime.proxmox.list_vms().await?;
    for vm in inventory.into_iter().filter(|vm| !vm.is_template) {
        let Some(resource_text) = vm.name.strip_prefix("aurora-") else {
            continue;
        };
        let Ok(resource_id) = Uuid::parse_str(resource_text) else {
            continue;
        };
        if resource_id.is_nil() || vm.name != format!("aurora-{resource_id}") {
            continue;
        }
        record_observation(
            zone_kv,
            resource_id,
            now_ms,
            vm.network_in_bytes,
            vm.network_out_bytes,
            false,
        )
        .await?;
    }
    publish_closed_state_pages(settings, kafka, zone_kv, now_ms, state_scan_skip).await?;
    Ok(())
}

pub(super) async fn record_network_observation(
    zone_kv: &ZoneKvStore,
    resource_id: Uuid,
    observed_at_ms: i64,
    network_in_bytes: u64,
    network_out_bytes: u64,
) -> Result<(), String> {
    record_observation(
        zone_kv,
        resource_id,
        observed_at_ms,
        network_in_bytes,
        network_out_bytes,
        false,
    )
    .await
}

pub(super) async fn record_terminal_network_observation(
    zone_kv: &ZoneKvStore,
    resource_id: Uuid,
    observed_at_ms: i64,
    network_in_bytes: u64,
    network_out_bytes: u64,
) -> Result<(), String> {
    record_observation(
        zone_kv,
        resource_id,
        observed_at_ms,
        network_in_bytes,
        network_out_bytes,
        true,
    )
    .await
}

async fn record_observation(
    zone_kv: &ZoneKvStore,
    resource_id: Uuid,
    observed_at_ms: i64,
    network_in_bytes: u64,
    network_out_bytes: u64,
    terminal: bool,
) -> Result<(), String> {
    let key = format!("hypervisor.network.metering.{resource_id}");
    let entry = zone_kv.config_entry(key.clone()).await?;
    let (mut state, revision) = match entry {
        Some(entry) => {
            let state: MeteringState = serde_json::from_slice(&entry.value)
                .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_CORRUPT".to_string())?;
            if state.schema_version != SCHEMA_VERSION
                || state.resource_id != resource_id.to_string()
            {
                return Err("HYPERVISOR_NETWORK_METERING_STATE_IDENTITY_INVALID".to_string());
            }
            (state, entry.revision)
        }
        None => {
            let initial = MeteringState {
                schema_version: SCHEMA_VERSION,
                resource_id: resource_id.to_string(),
                last_observed_at_unix_ms: observed_at_ms,
                last_network_in_bytes: network_in_bytes,
                last_network_out_bytes: network_out_bytes,
                pending_windows: Vec::new(),
                retired_at_unix_ms: terminal.then_some(observed_at_ms),
            };
            let encoded = serde_json::to_vec(&initial)
                .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_ENCODE_FAILED".to_string())?;
            match zone_kv.config_create(&key, Bytes::from(encoded)).await {
                Ok(_) => return Ok(()),
                Err(_) => {
                    return Err("HYPERVISOR_NETWORK_METERING_STATE_CREATE_CONFLICT".to_string());
                }
            }
        }
    };

    if observed_at_ms > state.last_observed_at_unix_ms
        && network_in_bytes >= state.last_network_in_bytes
        && network_out_bytes >= state.last_network_out_bytes
    {
        allocate_delta(
            &mut state.pending_windows,
            state.last_observed_at_unix_ms,
            observed_at_ms,
            network_in_bytes - state.last_network_in_bytes,
            network_out_bytes - state.last_network_out_bytes,
        )?;
    }
    if observed_at_ms >= state.last_observed_at_unix_ms {
        state.last_observed_at_unix_ms = observed_at_ms;
        state.last_network_in_bytes = network_in_bytes;
        state.last_network_out_bytes = network_out_bytes;
    }
    state.retired_at_unix_ms = terminal.then_some(observed_at_ms);
    state
        .pending_windows
        .sort_by_key(|window| window.start_unix_ms);
    let encoded = serde_json::to_vec(&state)
        .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_ENCODE_FAILED".to_string())?;
    zone_kv
        .config_update(&key, Bytes::from(encoded), revision)
        .await?;

    Ok(())
}

async fn publish_closed_state_pages(
    settings: &Settings,
    kafka: &KafkaTransport,
    zone_kv: &ZoneKvStore,
    observed_at_ms: i64,
    state_scan_skip: &mut usize,
) -> Result<(), String> {
    for _ in 0..settings.state_scan_max_pages {
        let (keys, has_more) = zone_kv
            .config_keys_page(*state_scan_skip, settings.state_scan_page_size)
            .await?;
        if keys.is_empty() {
            *state_scan_skip = 0;
            return Ok(());
        }
        *state_scan_skip = state_scan_skip.saturating_add(keys.len());
        for key in keys
            .iter()
            .filter(|key| key.starts_with("hypervisor.network.metering."))
        {
            publish_closed_for_key(settings, kafka, zone_kv, key, observed_at_ms).await?;
        }
        if !has_more {
            *state_scan_skip = 0;
            return Ok(());
        }
    }
    Ok(())
}

async fn publish_closed_for_key(
    settings: &Settings,
    kafka: &KafkaTransport,
    zone_kv: &ZoneKvStore,
    key: &str,
    observed_at_ms: i64,
) -> Result<(), String> {
    let Some(entry) = zone_kv.config_entry(key.to_string()).await? else {
        return Ok(());
    };
    let mut state: MeteringState = serde_json::from_slice(&entry.value)
        .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_CORRUPT".to_string())?;
    if state.schema_version != SCHEMA_VERSION {
        return Err("HYPERVISOR_NETWORK_METERING_STATE_VERSION_INVALID".to_string());
    }
    let resource_id = Uuid::parse_str(&state.resource_id)
        .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_IDENTITY_INVALID".to_string())?;
    let mut revision = entry.revision;

    let closed = state
        .pending_windows
        .iter()
        .filter(|window| window.start_unix_ms + HOUR_MS + settings.late_grace_ms <= observed_at_ms)
        .cloned()
        .collect::<Vec<_>>();
    for window in closed {
        let report = build_report(settings.zone_id, resource_id, &window)?;
        kafka
            .publish_message(
                &kafka.hypervisor_network_usage_reports_topic(),
                report.report_id.as_bytes(),
                &report,
            )
            .await?;
        state
            .pending_windows
            .retain(|pending| pending.start_unix_ms != window.start_unix_ms);
        let encoded = serde_json::to_vec(&state)
            .map_err(|_| "HYPERVISOR_NETWORK_METERING_STATE_ENCODE_FAILED".to_string())?;
        revision = zone_kv
            .config_update(&key, Bytes::from(encoded), revision)
            .await?;
    }
    if state.pending_windows.is_empty()
        && state
            .retired_at_unix_ms
            .is_some_and(|retired_at| retired_at.saturating_add(86_400_000) <= observed_at_ms)
    {
        zone_kv.config_delete(key).await?;
    }
    Ok(())
}

fn allocate_delta(
    pending: &mut Vec<PendingWindow>,
    start_ms: i64,
    end_ms: i64,
    network_in_bytes: u64,
    network_out_bytes: u64,
) -> Result<(), String> {
    let total_ms = end_ms.saturating_sub(start_ms);
    if total_ms <= 0 || (network_in_bytes == 0 && network_out_bytes == 0) {
        return Ok(());
    }
    let mut cursor = start_ms;
    let mut assigned_in = 0_u64;
    let mut assigned_out = 0_u64;
    while cursor < end_ms {
        let window_start = cursor.div_euclid(HOUR_MS) * HOUR_MS;
        let slice_end = end_ms.min(window_start + HOUR_MS);
        let slice_ms = slice_end - cursor;
        let is_last = slice_end == end_ms;
        let slice_in = if is_last {
            network_in_bytes.saturating_sub(assigned_in)
        } else {
            u64::try_from(
                u128::from(network_in_bytes) * u128::try_from(slice_ms).unwrap_or_default()
                    / u128::try_from(total_ms).unwrap_or(1),
            )
            .map_err(|_| "HYPERVISOR_NETWORK_IN_DELTA_OVERFLOW".to_string())?
        };
        let slice_out = if is_last {
            network_out_bytes.saturating_sub(assigned_out)
        } else {
            u64::try_from(
                u128::from(network_out_bytes) * u128::try_from(slice_ms).unwrap_or_default()
                    / u128::try_from(total_ms).unwrap_or(1),
            )
            .map_err(|_| "HYPERVISOR_NETWORK_OUT_DELTA_OVERFLOW".to_string())?
        };
        assigned_in = assigned_in
            .checked_add(slice_in)
            .ok_or_else(|| "HYPERVISOR_NETWORK_IN_DELTA_OVERFLOW".to_string())?;
        assigned_out = assigned_out
            .checked_add(slice_out)
            .ok_or_else(|| "HYPERVISOR_NETWORK_OUT_DELTA_OVERFLOW".to_string())?;
        if slice_in > 0 || slice_out > 0 {
            if let Some(window) = pending
                .iter_mut()
                .find(|window| window.start_unix_ms == window_start)
            {
                window.network_in_bytes = window
                    .network_in_bytes
                    .checked_add(slice_in)
                    .ok_or_else(|| "HYPERVISOR_NETWORK_IN_WINDOW_OVERFLOW".to_string())?;
                window.network_out_bytes = window
                    .network_out_bytes
                    .checked_add(slice_out)
                    .ok_or_else(|| "HYPERVISOR_NETWORK_OUT_WINDOW_OVERFLOW".to_string())?;
            } else {
                pending.push(PendingWindow {
                    start_unix_ms: window_start,
                    network_in_bytes: slice_in,
                    network_out_bytes: slice_out,
                });
            }
        }
        cursor = slice_end;
    }
    Ok(())
}

fn build_report(
    zone_id: Uuid,
    resource_id: Uuid,
    window: &PendingWindow,
) -> Result<HypervisorNetworkUsageReportV1, String> {
    let window_end = window.start_unix_ms.saturating_add(HOUR_MS);
    let sequence = u64::try_from(window_end.div_euclid(HOUR_MS))
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_SEQUENCE_INVALID".to_string())?;
    let report_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{zone_id}:{resource_id}:{}:{window_end}:{sequence}",
            window.start_unix_ms
        )
        .as_bytes(),
    );
    let mut report = HypervisorNetworkUsageReportV1 {
        schema_version: SCHEMA_VERSION,
        report_id: report_id.to_string(),
        zone_id: zone_id.to_string(),
        resource_id: resource_id.to_string(),
        window_start_unix_ms: window.start_unix_ms,
        window_end_unix_ms: window_end,
        sequence,
        network_in_bytes: window.network_in_bytes,
        network_out_bytes: window.network_out_bytes,
        report_sha256: Vec::new(),
    };
    report.report_sha256 = Sha256::digest(report.encode_to_vec()).to_vec();
    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn crossing_hour_preserves_every_integer_byte() {
        let mut windows = Vec::new();
        allocate_delta(&mut windows, HOUR_MS - 1_000, HOUR_MS + 1_000, 101, 203).unwrap();
        assert_eq!(windows.len(), 2);
        assert_eq!(
            windows
                .iter()
                .map(|window| window.network_in_bytes)
                .sum::<u64>(),
            101
        );
        assert_eq!(
            windows
                .iter()
                .map(|window| window.network_out_bytes)
                .sum::<u64>(),
            203
        );
        assert_eq!(windows[0].network_in_bytes, 50);
        assert_eq!(windows[1].network_in_bytes, 51);
    }

    #[test]
    fn report_identity_and_checksum_are_deterministic() {
        let zone = Uuid::new_v4();
        let resource = Uuid::new_v4();
        let window = PendingWindow {
            start_unix_ms: HOUR_MS,
            network_in_bytes: 7,
            network_out_bytes: 11,
        };
        let first = build_report(zone, resource, &window).unwrap();
        let second = build_report(zone, resource, &window).unwrap();
        assert_eq!(first.report_id, second.report_id);
        assert_eq!(first.report_sha256, second.report_sha256);
    }
}
