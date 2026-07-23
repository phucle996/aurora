use super::super::MailRuntime;
use super::backpressure::MailBackpressureSnapshot;
use super::metrics::MailOperationalMetricsSnapshot;
use crate::config::Config;
use crate::executor::mail::runtime::RuntimeHealthSnapshot;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use bytes::Bytes;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use uuid::Uuid;

#[derive(Clone, Deserialize, Serialize)]
struct LocalMailNodeSnapshot {
    node_id: String,
    boot_id: String,
    pending_items: usize,
    in_flight_batches: usize,
    queue_capacity: usize,
    jmap_reachable: bool,
    last_probe_at_unix_ms: u64,
    observed_at_unix_ms: u64,
}

#[derive(Serialize)]
struct ZoneMailHealthSnapshot<'a> {
    status: &'a str,
    capacity: usize,
    pending_items: usize,
    in_flight_batches: usize,
    transport: &'static str,
    updated_at: u64,
    fencing_token: u64,
    probe_node_id: &'a str,
}

/// [COMMENT]: Mỗi pod ghi local health; rotating winner probe JMAP/Stalwart, ghi fenced Zone KV
/// và OTel aggregates. Không có reverse stream, PostgreSQL projection hoặc customer labels.
pub(super) fn start(config: Arc<Config>, zone_kv: Arc<ZoneKvStore>, runtime: Arc<MailRuntime>) {
    tokio::spawn(async move {
        let node_id = runtime.runtime_node_id.clone();
        let boot_id = runtime.runtime_boot_id;
        let management_client = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(5))
            .pool_idle_timeout(Duration::from_secs(90))
            .tcp_keepalive(Duration::from_secs(30))
            .build()
            .ok();
        let mut last_jmap_reachable = false;
        let mut last_probe_at_unix_ms = 0_u64;
        let mut last_probe_success_unix_seconds = 0_u64;

        loop {
            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let pending = runtime.metrics.pending_items.load(Ordering::Relaxed);
            let in_flight = runtime.metrics.in_flight_batches.load(Ordering::Relaxed);
            let local = LocalMailNodeSnapshot {
                node_id: node_id.clone(),
                boot_id: boot_id.to_string(),
                pending_items: pending,
                in_flight_batches: in_flight,
                queue_capacity: config.mail_batch_queue_capacity,
                jmap_reachable: last_jmap_reachable,
                last_probe_at_unix_ms,
                observed_at_unix_ms: now_ms,
            };
            if let Ok(value) = serde_json::to_vec(&local) {
                // [COMMENT]: Physical pod chỉ ghi key của chính nó; boot UUID làm restart visible.
                let _ = zone_kv
                    .health_put(format!("mail.health.node.{node_id}"), Bytes::from(value))
                    .await;
            }

            let lease = match zone_kv
                .acquire_rotating_lease(
                    "lease.mail.health.observe",
                    &node_id,
                    Duration::from_secs(20),
                    Duration::from_millis(
                        config.mail_health_observe_interval_ms.saturating_add(2_000),
                    ),
                )
                .await
            {
                Ok(Some(lease)) => lease,
                Ok(None) => {
                    tokio::time::sleep(Duration::from_millis(
                        config.mail_health_observe_interval_ms + rand::random::<u64>() % 2_000,
                    ))
                    .await;
                    continue;
                }
                Err(error) => {
                    Logger::sys_warn(
                        "mail.supervisor.health_observer",
                        "Failed to acquire rotating Mail health lease",
                        &error,
                    );
                    tokio::time::sleep(Duration::from_secs(2)).await;
                    continue;
                }
            };

            last_probe_at_unix_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let probe_started = Instant::now();
            last_jmap_reachable =
                tokio::time::timeout(Duration::from_secs(3), runtime.healthcheck())
                    .await
                    .is_ok_and(|result| result.is_ok());
            let probe_duration_seconds = probe_started.elapsed().as_secs_f64();
            if last_jmap_reachable {
                last_probe_success_unix_seconds = last_probe_at_unix_ms / 1_000;
            }

            // [COMMENT]: Management identity chỉ có ClusterNode query/get; metric chỉ xuất bounded count theo state.
            let mut inventory_error = String::new();
            let mut stalwart_active = 0_u64;
            let mut stalwart_stale = 0_u64;
            let mut stalwart_inactive = 0_u64;
            if config.stalwart_management_jmap_url.trim().is_empty()
                || config.stalwart_reporter_bearer_token.trim().is_empty()
            {
                inventory_error = "MAIL_STALWART_INVENTORY_UNCONFIGURED".to_string();
            } else if let Some(client) = &management_client {
                let request = json!({
                    "using": ["urn:ietf:params:jmap:core", "urn:stalwart:jmap"],
                    "methodCalls": [
                        ["x:ClusterNode/query", {"filter": {}}, "cluster-query"],
                        ["x:ClusterNode/get", {
                            "#ids": {
                                "resultOf": "cluster-query",
                                "name": "x:ClusterNode/query",
                                "path": "/ids"
                            }
                        }, "cluster-get"]
                    ]
                });
                match client
                    .post(config.stalwart_management_jmap_url.trim())
                    .bearer_auth(config.stalwart_reporter_bearer_token.trim())
                    .json(&request)
                    .send()
                    .await
                {
                    Ok(response) if response.status().is_success() => {
                        match response.json::<Value>().await {
                            Ok(value) => {
                                if let Some(methods) =
                                    value.get("methodResponses").and_then(Value::as_array)
                                {
                                    for method in methods {
                                        let Some(parts) = method.as_array() else {
                                            continue;
                                        };
                                        if parts.len() < 2
                                            || parts[0].as_str() != Some("x:ClusterNode/get")
                                        {
                                            continue;
                                        }
                                        let Some(nodes) =
                                            parts[1].get("list").and_then(Value::as_array)
                                        else {
                                            continue;
                                        };
                                        for node in nodes.iter().take(512) {
                                            if node.get("nodeId").and_then(Value::as_u64).is_none()
                                            {
                                                continue;
                                            }
                                            match node.get("status").and_then(Value::as_str) {
                                                Some("active") => {
                                                    stalwart_active =
                                                        stalwart_active.saturating_add(1)
                                                }
                                                Some("stale") => {
                                                    stalwart_stale =
                                                        stalwart_stale.saturating_add(1)
                                                }
                                                Some("inactive") => {
                                                    stalwart_inactive =
                                                        stalwart_inactive.saturating_add(1)
                                                }
                                                _ => {}
                                            }
                                        }
                                        if nodes.len() > 512 {
                                            inventory_error =
                                                "MAIL_STALWART_INVENTORY_TRUNCATED".to_string();
                                        }
                                    }
                                }
                                if stalwart_active + stalwart_stale + stalwart_inactive == 0
                                    && inventory_error.is_empty()
                                {
                                    inventory_error = "MAIL_STALWART_INVENTORY_EMPTY".to_string();
                                }
                            }
                            Err(_) => {
                                inventory_error = "MAIL_STALWART_INVENTORY_INVALID".to_string()
                            }
                        }
                    }
                    Ok(_) => inventory_error = "MAIL_STALWART_INVENTORY_HTTP".to_string(),
                    Err(_) => inventory_error = "MAIL_STALWART_INVENTORY_UNAVAILABLE".to_string(),
                }
            } else {
                inventory_error = "MAIL_STALWART_OBSERVER_CLIENT_UNAVAILABLE".to_string();
            }

            let refreshed_local = LocalMailNodeSnapshot {
                node_id: node_id.clone(),
                boot_id: boot_id.to_string(),
                pending_items: pending,
                in_flight_batches: in_flight,
                queue_capacity: config.mail_batch_queue_capacity,
                jmap_reachable: last_jmap_reachable,
                last_probe_at_unix_ms,
                observed_at_unix_ms: last_probe_at_unix_ms,
            };
            if let Ok(value) = serde_json::to_vec(&refreshed_local) {
                let _ = zone_kv
                    .health_put(format!("mail.health.node.{node_id}"), Bytes::from(value))
                    .await;
            }

            let aggregate_at_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let health_keys = zone_kv.health_keys().await.unwrap_or_default();
            let mut active_consumer_slots = 0_u64;
            for key in &health_keys {
                if !key.starts_with("mail.runtime.") {
                    continue;
                }
                let Some(bytes) = zone_kv.health_get(key.clone()).await.ok().flatten() else {
                    continue;
                };
                let Ok(slot) = serde_json::from_slice::<RuntimeHealthSnapshot>(&bytes) else {
                    continue;
                };
                let slot_key_matches = Uuid::parse_str(&slot.consumer_id).is_ok()
                    && key == &format!("mail.runtime.{}.{}", slot.consumer_id, slot.slot);
                let slot_is_live = matches!(
                    slot.state.as_str(),
                    "STARTING" | "RUNNING" | "PAUSED" | "DRAINING" | "DEGRADED"
                );
                if slot_key_matches
                    && slot_is_live
                    && slot.runtime_generation > 0
                    && slot.runtime_generation == slot.fencing_token
                    && !slot.runtime_node_id.is_empty()
                    && aggregate_at_ms.saturating_sub(slot.heartbeat_unix_ms)
                        <= config
                            .mail_stream_slot_lease_ttl_seconds
                            .saturating_mul(2_000)
                {
                    active_consumer_slots = active_consumer_slots.saturating_add(1);
                }
            }

            let freshness_ms = config.mail_health_observe_interval_ms.saturating_mul(3);
            let mut node_snapshots = Vec::<LocalMailNodeSnapshot>::new();
            for key in &health_keys {
                if !key.starts_with("mail.health.node.") {
                    continue;
                }
                let Some(bytes) = zone_kv.health_get(key.clone()).await.ok().flatten() else {
                    continue;
                };
                let Ok(node) = serde_json::from_slice::<LocalMailNodeSnapshot>(&bytes) else {
                    continue;
                };
                if key.strip_prefix("mail.health.node.") == Some(node.node_id.as_str())
                    && aggregate_at_ms.saturating_sub(node.observed_at_unix_ms) <= freshness_ms
                    && Uuid::parse_str(&node.boot_id).is_ok()
                    && !node.node_id.is_empty()
                    && node.node_id.len() <= 255
                    && !node.node_id.chars().any(char::is_control)
                {
                    node_snapshots.push(node);
                }
            }
            node_snapshots.sort_by(|left, right| left.node_id.cmp(&right.node_id));
            let inventory_truncated = node_snapshots.len() > 512
                || inventory_error == "MAIL_STALWART_INVENTORY_TRUNCATED";
            node_snapshots.truncate(512);

            let total_pending = node_snapshots.iter().fold(0_usize, |total, node| {
                total.saturating_add(node.pending_items)
            });
            let total_in_flight = node_snapshots.iter().fold(0_usize, |total, node| {
                total.saturating_add(node.in_flight_batches)
            });
            let total_queue_capacity = node_snapshots.iter().fold(0_usize, |total, node| {
                total.saturating_add(node.queue_capacity)
            });
            let metadata = zone_kv.read_zone_metadata().await.unwrap_or_default();
            let disabled = metadata.status == "disabled"
                || !metadata.services.get("mail").copied().unwrap_or(false);
            let reachable_nodes = node_snapshots
                .iter()
                .filter(|node| {
                    node.jmap_reachable
                        && aggregate_at_ms.saturating_sub(node.last_probe_at_unix_ms)
                            <= freshness_ms.saturating_mul(2)
                })
                .count();
            let failed_probe_nodes = node_snapshots
                .iter()
                .filter(|node| {
                    !node.jmap_reachable
                        && node.last_probe_at_unix_ms > 0
                        && aggregate_at_ms.saturating_sub(node.last_probe_at_unix_ms)
                            <= freshness_ms.saturating_mul(2)
                })
                .count();
            let pressure = MailBackpressureSnapshot::calculate(
                disabled,
                reachable_nodes > 0,
                total_pending,
                total_queue_capacity.max(1),
            );
            let (service_state_name, service_state_value) = if disabled || reachable_nodes == 0 {
                ("down", 0_u64)
            } else if pressure.status == "degraded" || failed_probe_nodes > 0 || inventory_truncated
            {
                ("degraded", 1_u64)
            } else {
                ("healthy", 2_u64)
            };

            let mut dataplane_healthy = 0_u64;
            let mut dataplane_degraded = 0_u64;
            let mut dataplane_down = 0_u64;
            for node in &node_snapshots {
                let probe_fresh = node.last_probe_at_unix_ms > 0
                    && aggregate_at_ms.saturating_sub(node.last_probe_at_unix_ms)
                        <= freshness_ms.saturating_mul(2);
                if node.jmap_reachable && probe_fresh {
                    dataplane_healthy = dataplane_healthy.saturating_add(1);
                } else if probe_fresh {
                    dataplane_degraded = dataplane_degraded.saturating_add(1);
                } else {
                    dataplane_down = dataplane_down.saturating_add(1);
                }
            }

            let zone_health = ZoneMailHealthSnapshot {
                status: service_state_name,
                capacity: pressure.capacity.min(100),
                pending_items: total_pending,
                in_flight_batches: total_in_flight,
                transport: "jmap_batch",
                updated_at: aggregate_at_ms / 1_000,
                fencing_token: lease.fencing_token,
                probe_node_id: &node_id,
            };

            // [COMMENT]: Renew sát OTel/KV side effects; holder cũ không quảng cáo health sau lease loss.
            if zone_kv
                .renew_lease(&lease, Duration::from_secs(20))
                .await
                .unwrap_or(false)
            {
                runtime.metrics.record_jmap_probe(
                    last_jmap_reachable,
                    probe_duration_seconds,
                    last_probe_success_unix_seconds,
                );
                runtime
                    .metrics
                    .record_operational_snapshot(&MailOperationalMetricsSnapshot {
                        state: service_state_value,
                        capacity_percent: pressure.capacity.min(100) as u64,
                        pending_items: total_pending.min(u64::MAX as usize) as u64,
                        in_flight_batches: total_in_flight.min(u64::MAX as usize) as u64,
                        active_consumer_slots,
                        dataplane_nodes_healthy: dataplane_healthy,
                        dataplane_nodes_degraded: dataplane_degraded,
                        dataplane_nodes_down: dataplane_down,
                        stalwart_nodes_active: stalwart_active,
                        stalwart_nodes_stale: stalwart_stale,
                        stalwart_nodes_inactive: stalwart_inactive,
                    });
                runtime.metrics.record_observation_error(&inventory_error);
                if let Ok(value) = serde_json::to_vec(&zone_health) {
                    let _ = zone_kv
                        .health_put_fenced(
                            "zone.service.mail",
                            Bytes::from(value),
                            lease.fencing_token,
                        )
                        .await;
                }
            }

            let _ = zone_kv.release_lease(&lease).await;
            tokio::time::sleep(Duration::from_millis(
                config.mail_health_observe_interval_ms + rand::random::<u64>() % 2_000,
            ))
            .await;
        }
    });
}
