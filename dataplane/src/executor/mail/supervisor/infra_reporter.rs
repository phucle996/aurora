use super::super::MailRuntime;
use super::backpressure::MailBackpressureSnapshot;
use crate::config::Config;
use crate::executor::mail::runtime::RuntimeHealthSnapshot;
use crate::executor::mail::runtime_proto::{
    MailDataplaneNodeSnapshotV1, MailEventMetadataV1, MailInfrastructureSnapshotReportedV1,
    MailInfrastructureState, MailStalwartNodeSnapshotV1, MailStalwartNodeState,
};
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use bytes::Bytes;
use chrono::DateTime;
use prost::Message;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
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
    error_code: String,
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

/// [COMMENT]: Mỗi pod ghi local snapshot, nhưng chỉ rotating lease holder probe Stalwart,
/// aggregate inventory, ghi fenced Zone health và publish đúng một infra snapshot mỗi chu kỳ.
pub(super) fn start(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
    runtime: Arc<MailRuntime>,
) {
    tokio::spawn(async move {
        let node_id = runtime.runtime_node_id.clone();
        let boot_id = runtime.runtime_boot_id;
        let event_namespace = Uuid::parse_str("3b614680-15ef-47ab-a7d5-36ae3f03666a")
            .expect("mail infrastructure report namespace must be valid");
        let management_client = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(5))
            .pool_idle_timeout(Duration::from_secs(90))
            .tcp_keepalive(Duration::from_secs(30))
            .build()
            .ok();
        let mut redis_connection: Option<redis::aio::MultiplexedConnection> = None;
        let mut last_jmap_reachable = false;
        let mut last_probe_at_unix_ms = 0_u64;
        let mut last_probe_error = "MAIL_JMAP_NOT_PROBED".to_string();

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
                error_code: last_probe_error.clone(),
            };
            if let Ok(value) = serde_json::to_vec(&local) {
                // [COMMENT]: Mỗi physical node chỉ sở hữu key của chính nó; boot UUID làm restart visible.
                let _ = zone_kv
                    .health_put(format!("mail.infra.node.{}", node_id), Bytes::from(value))
                    .await;
            }

            let lease = match zone_kv
                .acquire_rotating_lease(
                    "lease.mail.infra.report",
                    &node_id,
                    Duration::from_secs(20),
                    Duration::from_millis(
                        config.mail_infra_report_interval_ms.saturating_add(2_000),
                    ),
                )
                .await
            {
                Ok(Some(lease)) => lease,
                Ok(None) => {
                    tokio::time::sleep(Duration::from_millis(
                        config.mail_infra_report_interval_ms + rand::random::<u64>() % 2_000,
                    ))
                    .await;
                    continue;
                }
                Err(error) => {
                    Logger::sys_warn(
                        "mail.supervisor.infra_reporter",
                        "Failed to acquire Mail infrastructure report lease",
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
            last_jmap_reachable =
                tokio::time::timeout(Duration::from_secs(3), runtime.healthcheck())
                    .await
                    .is_ok_and(|result| result.is_ok());
            last_probe_error = if last_jmap_reachable {
                String::new()
            } else {
                "MAIL_JMAP_UNREACHABLE".to_string()
            };

            // [COMMENT]: Cluster registry dùng management identity riêng chỉ có query/get.
            // Missing optional integration không làm delivery fail; Admin vẫn thấy inventory unavailable.
            let mut inventory_error = String::new();
            let mut stalwart_nodes = Vec::<MailStalwartNodeSnapshotV1>::new();
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
                                            let Some(stalwart_node_id) =
                                                node.get("nodeId").and_then(Value::as_u64)
                                            else {
                                                continue;
                                            };
                                            let hostname = node
                                                .get("hostname")
                                                .and_then(Value::as_str)
                                                .unwrap_or_default()
                                                .trim();
                                            if hostname.is_empty()
                                                || hostname.len() > 255
                                                || hostname.chars().any(char::is_control)
                                            {
                                                continue;
                                            }
                                            let state = match node
                                                .get("status")
                                                .and_then(Value::as_str)
                                            {
                                                Some("active") => MailStalwartNodeState::Active,
                                                Some("stale") => MailStalwartNodeState::Stale,
                                                Some("inactive") => MailStalwartNodeState::Inactive,
                                                _ => continue,
                                            };
                                            let last_renewal_unix_ms = node
                                                .get("lastRenewal")
                                                .and_then(Value::as_str)
                                                .and_then(|value| {
                                                    DateTime::parse_from_rfc3339(value).ok()
                                                })
                                                .map(|value| value.timestamp_millis())
                                                .unwrap_or_default();
                                            stalwart_nodes.push(MailStalwartNodeSnapshotV1 {
                                                node_id: stalwart_node_id,
                                                hostname: hostname.to_string(),
                                                state: state as i32,
                                                last_renewal_unix_ms,
                                            });
                                        }
                                        if nodes.len() > 512 {
                                            inventory_error =
                                                "MAIL_STALWART_INVENTORY_TRUNCATED".to_string();
                                        }
                                    }
                                }
                                if stalwart_nodes.is_empty() && inventory_error.is_empty() {
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
                inventory_error = "MAIL_STALWART_REPORTER_CLIENT_UNAVAILABLE".to_string();
            }

            // [COMMENT]: Winner refresh local probe result rồi aggregate mọi fresh physical node.
            let refreshed_local = LocalMailNodeSnapshot {
                node_id: node_id.clone(),
                boot_id: boot_id.to_string(),
                pending_items: pending,
                in_flight_batches: in_flight,
                queue_capacity: config.mail_batch_queue_capacity,
                jmap_reachable: last_jmap_reachable,
                last_probe_at_unix_ms,
                observed_at_unix_ms: last_probe_at_unix_ms,
                error_code: last_probe_error.clone(),
            };
            if let Ok(value) = serde_json::to_vec(&refreshed_local) {
                let _ = zone_kv
                    .health_put(format!("mail.infra.node.{}", node_id), Bytes::from(value))
                    .await;
            }

            let aggregate_at_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let mut slot_count_by_node = HashMap::<String, u32>::new();
            let health_keys = zone_kv.health_keys().await.unwrap_or_default();
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
                    && slot.runtime_node_id.len() <= 255
                    && !slot.runtime_node_id.chars().any(char::is_control)
                    && aggregate_at_ms.saturating_sub(slot.heartbeat_unix_ms)
                        <= config
                            .mail_stream_slot_lease_ttl_seconds
                            .saturating_mul(2_000)
                {
                    let count = slot_count_by_node.entry(slot.runtime_node_id).or_default();
                    *count = count.saturating_add(1);
                }
            }

            let freshness_ms = config.mail_infra_report_interval_ms.saturating_mul(3);
            let mut node_snapshots = Vec::<LocalMailNodeSnapshot>::new();
            for key in &health_keys {
                if !key.starts_with("mail.infra.node.") {
                    continue;
                }
                let Some(bytes) = zone_kv.health_get(key.clone()).await.ok().flatten() else {
                    continue;
                };
                let Ok(node) = serde_json::from_slice::<LocalMailNodeSnapshot>(&bytes) else {
                    continue;
                };
                if key.strip_prefix("mail.infra.node.") == Some(node.node_id.as_str())
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
                // [COMMENT]: Metadata/service chưa hydrate phải fail-closed; health probe vẫn chạy
                // để SRE quan sát nhưng snapshot không được quảng cáo capacity nhận tải.
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
            let service_state = if disabled || reachable_nodes == 0 {
                MailInfrastructureState::Down
            } else if pressure.status == "degraded" || failed_probe_nodes > 0 || inventory_truncated
            {
                MailInfrastructureState::Degraded
            } else {
                MailInfrastructureState::Healthy
            };
            let service_state_name = match service_state {
                MailInfrastructureState::Healthy => "healthy",
                MailInfrastructureState::Degraded => "degraded",
                MailInfrastructureState::Unhealthy => "unhealthy",
                MailInfrastructureState::Down | MailInfrastructureState::Unspecified => "down",
            };
            let dataplane_nodes = node_snapshots
                .into_iter()
                .filter_map(|node| {
                    let parsed_boot_id = Uuid::parse_str(&node.boot_id).ok()?;
                    let probe_fresh = node.last_probe_at_unix_ms > 0
                        && aggregate_at_ms.saturating_sub(node.last_probe_at_unix_ms)
                            <= freshness_ms.saturating_mul(2);
                    let state = if node.jmap_reachable && probe_fresh {
                        MailInfrastructureState::Healthy
                    } else if !probe_fresh {
                        MailInfrastructureState::Unhealthy
                    } else {
                        MailInfrastructureState::Degraded
                    };
                    let capacity = MailBackpressureSnapshot::calculate(
                        disabled,
                        node.jmap_reachable,
                        node.pending_items,
                        node.queue_capacity,
                    )
                    .capacity;
                    Some(MailDataplaneNodeSnapshotV1 {
                        node_id: node.node_id.clone(),
                        boot_id: parsed_boot_id.as_bytes().to_vec(),
                        state: state as i32,
                        capacity: capacity.min(100) as u32,
                        pending_items: node.pending_items.min(u64::MAX as usize) as u64,
                        in_flight_batches: node.in_flight_batches.min(u64::MAX as usize) as u64,
                        active_consumer_slots: slot_count_by_node
                            .get(&node.node_id)
                            .copied()
                            .unwrap_or_default(),
                        jmap_reachable: node.jmap_reachable && probe_fresh,
                        last_probe_at_unix_ms: node.last_probe_at_unix_ms.min(i64::MAX as u64)
                            as i64,
                        observed_at_unix_ms: node.observed_at_unix_ms.min(i64::MAX as u64) as i64,
                        error_code: node.error_code,
                    })
                })
                .collect::<Vec<_>>();

            let event_name = format!("{}:{}:1", config.zone_id, lease.fencing_token);
            let event_id = Uuid::new_v5(&event_namespace, event_name.as_bytes());
            let report = MailInfrastructureSnapshotReportedV1 {
                metadata: Some(MailEventMetadataV1 {
                    event_id: event_id.as_bytes().to_vec(),
                    schema_version: 1,
                    occurred_at_unix_ms: aggregate_at_ms.min(i64::MAX as u64) as i64,
                    traceparent: String::new(),
                    producer: "dataplane-mail-infra".to_string(),
                }),
                report_generation: lease.fencing_token,
                report_sequence: 1,
                service_state: service_state as i32,
                capacity: pressure.capacity.min(100) as u32,
                pending_items: total_pending.min(u64::MAX as usize) as u64,
                in_flight_batches: total_in_flight.min(u64::MAX as usize) as u64,
                probe_node_id: node_id.clone(),
                dataplane_nodes,
                stalwart_nodes,
                inventory_truncated,
                error_code: inventory_error,
            };
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

            // [COMMENT]: Renew sát side effects; stale lease không được ghi KV hoặc central stream.
            if zone_kv
                .renew_lease(&lease, Duration::from_secs(20))
                .await
                .unwrap_or(false)
            {
                if let Ok(value) = serde_json::to_vec(&zone_health) {
                    let _ = zone_kv
                        .health_put_fenced(
                            "zone.service.mail",
                            Bytes::from(value),
                            lease.fencing_token,
                        )
                        .await;
                }
                let mut payload = Vec::with_capacity(report.encoded_len());
                if report.encode(&mut payload).is_ok() && payload.len() <= 1 << 20 {
                    if redis_connection.is_none() {
                        redis_connection = redis_job
                            .client()
                            .get_multiplexed_tokio_connection()
                            .await
                            .ok();
                    }
                    if zone_kv
                        .renew_lease(&lease, Duration::from_secs(20))
                        .await
                        .unwrap_or(false)
                    {
                        if let Some(connection) = redis_connection.as_mut() {
                            let published: redis::RedisResult<String> = redis::cmd("XADD")
                                .arg("mail:infra:reports")
                                .arg("MINID")
                                .arg("~")
                                .arg(format!("{}-0", aggregate_at_ms.saturating_sub(3_600_000)))
                                .arg("*")
                                .arg("zone_id")
                                .arg(&config.zone_id)
                                .arg("payload")
                                .arg(payload)
                                .query_async(connection)
                                .await;
                            if published.is_err() {
                                redis_connection = None;
                            }
                        }
                    }
                } else {
                    Logger::sys_warn(
                        "mail.supervisor.infra_reporter",
                        "Mail infrastructure snapshot exceeded its bounded contract",
                        "MAIL_INFRA_REPORT_TOO_LARGE",
                    );
                }
            }

            let _ = zone_kv.release_lease(&lease).await;
            tokio::time::sleep(Duration::from_millis(
                config.mail_infra_report_interval_ms + rand::random::<u64>() % 2_000,
            ))
            .await;
        }
    });
}
