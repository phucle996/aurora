use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use bytes::Bytes;
use serde::Serialize;
use serde_json::{json, Value};
use uuid::Uuid;

use super::zone_leader_session::ZoneLeaderSession;
use crate::config::Config;
use crate::executor::mail::supervisor::{
    LocalMailNodeSnapshot, MailBackpressureSnapshot, MailOperationalMetricsSnapshot,
};
use crate::executor::mail::MailRuntime;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};

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

#[derive(Default)]
struct StalwartInventory {
    active: u64,
    stale: u64,
    inactive: u64,
    error_code: String,
}

/// [COMMENT]: JMAP echo và Stalwart inventory là Zone-wide infrastructure probes;
/// chỉ stable leader session được phép chạy và ghi aggregate.
pub(crate) async fn run_mail_infrastructure_health_probe(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    runtime: Arc<MailRuntime>,
) {
    let management_client = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(3))
        .timeout(Duration::from_secs(5))
        .pool_idle_timeout(Duration::from_secs(90))
        .tcp_keepalive(Duration::from_secs(30))
        .build()
        .ok();
    let mut last_probe_success_unix_seconds = 0_u64;

    loop {
        if !session.permits_external_side_effect().await {
            return;
        }
        let probe_started = Instant::now();
        let cancel = session.cancellation_token();
        let jmap_reachable = tokio::select! {
            _ = cancel.cancelled() => return,
            result = tokio::time::timeout(Duration::from_secs(3), runtime.healthcheck()) => {
                result.is_ok_and(|health| health.is_ok())
            }
        };
        let probe_duration_seconds = probe_started.elapsed().as_secs_f64();
        let aggregate_at_ms = current_unix_time_millis();
        if jmap_reachable {
            last_probe_success_unix_seconds = aggregate_at_ms / 1_000;
        }

        let inventory = if !session.permits_external_side_effect().await {
            return;
        } else {
            probe_stalwart_cluster_node_inventory(&config, management_client.as_ref(), &session)
                .await
        };
        let freshness_ms = config.mail_health_observe_interval_ms.saturating_mul(3);
        let mut node_snapshots =
            read_fresh_mail_dataplane_node_snapshots(&zone_kv, aggregate_at_ms, freshness_ms).await;
        let inventory_truncated = node_snapshots.len() > 512
            || inventory.error_code == "MAIL_STALWART_INVENTORY_TRUNCATED";
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
        let metadata = match zone_kv.read_zone_metadata().await {
            Ok(metadata) => metadata,
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.mail_infrastructure_health_probe",
                    "MAIL_ZONE_METADATA_READ_FAILED",
                    "Mail health probe cannot distinguish disabled from unavailable metadata; skipping aggregate write",
                    &error,
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        retryable: Some(true),
                        outcome: Some("skipped"),
                        ..LogFields::default()
                    },
                );
                if !session
                    .wait(Duration::from_millis(
                        config.mail_health_observe_interval_ms.max(1_000),
                    ))
                    .await
                {
                    return;
                }
                continue;
            }
        };
        let disabled = metadata.status == "disabled"
            || !metadata.services.get("mail").copied().unwrap_or(false);
        let pressure = MailBackpressureSnapshot::calculate(
            disabled,
            jmap_reachable && !node_snapshots.is_empty(),
            total_pending,
            total_queue_capacity.max(1),
        );
        let (service_state_name, service_state_value) =
            if disabled || !jmap_reachable || node_snapshots.is_empty() {
                ("down", 0_u64)
            } else if pressure.status == "degraded" || inventory_truncated {
                ("degraded", 1_u64)
            } else {
                ("healthy", 2_u64)
            };
        let node_count = node_snapshots.len().min(u64::MAX as usize) as u64;
        let (dataplane_healthy, dataplane_degraded, dataplane_down) = if disabled {
            (0, 0, node_count)
        } else if jmap_reachable {
            (node_count, 0, 0)
        } else {
            (0, node_count, 0)
        };

        let zone_health = ZoneMailHealthSnapshot {
            status: service_state_name,
            capacity: pressure.capacity.min(100),
            pending_items: total_pending,
            in_flight_batches: total_in_flight,
            transport: "jmap_batch",
            updated_at: aggregate_at_ms / 1_000,
            fencing_token: session.fencing_token(),
            probe_node_id: session.owner_id(),
        };
        if session.permits_external_side_effect().await {
            runtime.metrics.record_jmap_probe(
                jmap_reachable,
                probe_duration_seconds,
                last_probe_success_unix_seconds,
            );
            runtime
                .metrics
                .record_operational_snapshot(&MailOperationalMetricsSnapshot {
                    observed_at_unix_seconds: aggregate_at_ms / 1_000,
                    state: service_state_value,
                    capacity_percent: pressure.capacity.min(100) as u64,
                    pending_items: total_pending.min(u64::MAX as usize) as u64,
                    in_flight_batches: total_in_flight.min(u64::MAX as usize) as u64,
                    dataplane_nodes_healthy: dataplane_healthy,
                    dataplane_nodes_degraded: dataplane_degraded,
                    dataplane_nodes_down: dataplane_down,
                    stalwart_nodes_active: inventory.active,
                    stalwart_nodes_stale: inventory.stale,
                    stalwart_nodes_inactive: inventory.inactive,
                });
            runtime
                .metrics
                .record_observation_error(&inventory.error_code);
            match serde_json::to_vec(&zone_health) {
                Ok(value) => {
                    if let Err(error) = zone_kv
                        .health_put_fenced(
                            "zone.service.mail",
                            Bytes::from(value),
                            session.fencing_token(),
                        )
                        .await
                    {
                        Logger::sys_warn_with_fields(
                            "leader.mail_infrastructure_health_probe",
                            "MAIL_ZONE_HEALTH_WRITE_FAILED",
                            "Could not write fenced Zone mail health aggregate",
                            &error,
                            LogFields {
                                leader_fencing_token: Some(session.fencing_token()),
                                retryable: Some(true),
                                outcome: Some("stale"),
                                ..LogFields::default()
                            },
                        );
                    }
                }
                Err(error) => Logger::sys_error_with_fields(
                    "leader.mail_infrastructure_health_probe",
                    "MAIL_ZONE_HEALTH_SERIALIZE_FAILED",
                    "Could not serialize Zone mail health aggregate",
                    &error.to_string(),
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        outcome: Some("skipped"),
                        ..LogFields::default()
                    },
                ),
            }
        }

        if !session
            .wait(Duration::from_millis(
                config.mail_health_observe_interval_ms + rand::random::<u64>() % 2_000,
            ))
            .await
        {
            return;
        }
    }
}

async fn read_fresh_mail_dataplane_node_snapshots(
    zone_kv: &ZoneKvStore,
    now_ms: u64,
    freshness_ms: u64,
) -> Vec<LocalMailNodeSnapshot> {
    let mut snapshots = Vec::new();
    let keys = match zone_kv.health_keys().await {
        Ok(keys) => keys,
        Err(error) => {
            Logger::sys_warn(
                "leader.mail_infrastructure_health_probe",
                "Could not enumerate local mail node health snapshots",
                &error,
            );
            return snapshots;
        }
    };
    for key in keys {
        if !key.starts_with("mail.health.node.") {
            continue;
        }
        let bytes = match zone_kv.health_get(&key).await {
            Ok(Some(bytes)) => bytes,
            Ok(None) => continue,
            Err(error) => {
                Logger::sys_warn(
                    "leader.mail_infrastructure_health_probe",
                    &format!("Could not read local mail node snapshot key={key}"),
                    &error,
                );
                continue;
            }
        };
        let Ok(node) = serde_json::from_slice::<LocalMailNodeSnapshot>(&bytes) else {
            Logger::sys_warn(
                "leader.mail_infrastructure_health_probe",
                &format!("Ignored malformed local mail node snapshot key={key}"),
                "MAIL_LOCAL_NODE_SNAPSHOT_INVALID",
            );
            continue;
        };
        if key.strip_prefix("mail.health.node.") == Some(node.node_id.as_str())
            && now_ms.saturating_sub(node.observed_at_unix_ms) <= freshness_ms
            && Uuid::parse_str(&node.boot_id).is_ok()
            && !node.node_id.is_empty()
            && node.node_id.len() <= 255
            && !node.node_id.chars().any(char::is_control)
        {
            snapshots.push(node);
        } else {
            Logger::sys_warn(
                "leader.mail_infrastructure_health_probe",
                &format!("Ignored stale or contract-invalid local mail node snapshot key={key}"),
                "MAIL_LOCAL_NODE_SNAPSHOT_CONTRACT_INVALID",
            );
        }
    }
    snapshots.sort_by(|left, right| left.node_id.cmp(&right.node_id));
    snapshots
}

async fn probe_stalwart_cluster_node_inventory(
    config: &Config,
    client: Option<&reqwest::Client>,
    session: &ZoneLeaderSession,
) -> StalwartInventory {
    if config.stalwart_management_jmap_url.trim().is_empty()
        || config.stalwart_reporter_bearer_token.trim().is_empty()
    {
        return StalwartInventory {
            error_code: "MAIL_STALWART_INVENTORY_UNCONFIGURED".to_string(),
            ..Default::default()
        };
    }
    let Some(client) = client else {
        return StalwartInventory {
            error_code: "MAIL_STALWART_OBSERVER_CLIENT_UNAVAILABLE".to_string(),
            ..Default::default()
        };
    };
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
    let cancel = session.cancellation_token();
    let response = tokio::select! {
        _ = cancel.cancelled() => return StalwartInventory::default(),
        response = client
            .post(config.stalwart_management_jmap_url.trim())
            .bearer_auth(config.stalwart_reporter_bearer_token.trim())
            .json(&request)
            .send() => response,
    };
    let Ok(response) = response else {
        return StalwartInventory {
            error_code: "MAIL_STALWART_INVENTORY_UNAVAILABLE".to_string(),
            ..Default::default()
        };
    };
    if !response.status().is_success() {
        return StalwartInventory {
            error_code: "MAIL_STALWART_INVENTORY_HTTP".to_string(),
            ..Default::default()
        };
    }
    match response.json::<Value>().await {
        Ok(value) => parse_stalwart_cluster_node_inventory(&value),
        Err(_) => StalwartInventory {
            error_code: "MAIL_STALWART_INVENTORY_INVALID".to_string(),
            ..Default::default()
        },
    }
}

fn parse_stalwart_cluster_node_inventory(value: &Value) -> StalwartInventory {
    let mut result = StalwartInventory::default();
    let Some(methods) = value.get("methodResponses").and_then(Value::as_array) else {
        result.error_code = "MAIL_STALWART_INVENTORY_INVALID".to_string();
        return result;
    };
    for method in methods {
        let Some(parts) = method.as_array() else {
            continue;
        };
        if parts.len() < 2 || parts[0].as_str() != Some("x:ClusterNode/get") {
            continue;
        }
        let Some(nodes) = parts[1].get("list").and_then(Value::as_array) else {
            continue;
        };
        for node in nodes.iter().take(512) {
            if node.get("nodeId").and_then(Value::as_u64).is_none() {
                continue;
            }
            match node.get("status").and_then(Value::as_str) {
                Some("active") => result.active = result.active.saturating_add(1),
                Some("stale") => result.stale = result.stale.saturating_add(1),
                Some("inactive") => result.inactive = result.inactive.saturating_add(1),
                _ => {}
            }
        }
        if nodes.len() > 512 {
            result.error_code = "MAIL_STALWART_INVENTORY_TRUNCATED".to_string();
        }
    }
    if result.active + result.stale + result.inactive == 0 && result.error_code.is_empty() {
        result.error_code = "MAIL_STALWART_INVENTORY_EMPTY".to_string();
    }
    result
}

fn current_unix_time_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}
