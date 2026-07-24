use serde::Deserialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::session::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::leader::zone_report_proto;
use crate::observability::logger::{LogFields, Logger};

#[derive(Deserialize)]
struct NodeSnapshot {
    cpu: f64,
    ram: f64,
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

#[derive(Deserialize)]
struct ServiceSnapshot {
    status: String,
    capacity: usize,
}

#[derive(Deserialize)]
struct HypervisorCache {
    status: String,
    cpu_cores_total: i64,
    cpu_cores_used: i64,
    ram_mb_total: i64,
    ram_mb_used: i64,
    storage_gb_total: i64,
    storage_gb_used: i64,
    updated_at: i64,
}

#[derive(Deserialize)]
struct HypervisorSnapshot {
    nodes: HashMap<String, HypervisorCache>,
}

/// [COMMENT]: Leader aggregate per-node cached assignment lag; một consumer riêng không thể
/// đại diện toàn group vì krafka chỉ trả lag của partition đang assign cho process đó.
pub(crate) async fn run_zone_report_publisher(
    session: ZoneLeaderSession,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    config: Arc<Config>,
) {
    loop {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_secs())
            .unwrap_or_default();
        let mut total_cpu = 0.0;
        let mut total_ram = 0.0;
        let mut total_active_workers = 0_usize;
        let mut total_job_queue_lag = 0_u64;
        let mut job_queue_lag_stale = false;
        let mut alive_nodes = 0_usize;
        let health_keys = match zone_kv.health_keys().await {
            Ok(keys) => keys,
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.zone_report_publisher",
                    "ZONE_REPORT_HEALTH_KEYS_READ_FAILED",
                    "Cannot enumerate Zone health snapshots; skipping report instead of publishing an empty aggregate",
                    &error,
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        retryable: Some(true),
                        outcome: Some("skipped"),
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
            let bytes = match zone_kv.health_get(&key).await {
                Ok(Some(bytes)) => bytes,
                Ok(None) => continue,
                Err(error) => {
                    Logger::sys_warn(
                        "leader.zone_report_publisher",
                        &format!("Could not read Zone node snapshot key={key}"),
                        &error,
                    );
                    continue;
                }
            };
            let Ok(node) = serde_json::from_slice::<NodeSnapshot>(&bytes) else {
                Logger::sys_warn(
                    "leader.zone_report_publisher",
                    &format!("Ignored malformed Zone node snapshot key={key}"),
                    "ZONE_NODE_SNAPSHOT_INVALID",
                );
                continue;
            };
            if now.saturating_sub(node.updated_at) <= 15 {
                total_cpu += node.cpu;
                total_ram += node.ram;
                total_active_workers += node.active_workers;
                total_job_queue_lag = total_job_queue_lag.saturating_add(node.job_queue_lag);
                job_queue_lag_stale |= node.job_queue_lag_stale;
                alive_nodes += 1;
            }
        }
        if alive_nodes == 0 {
            job_queue_lag_stale = true;
        }

        let mail = read_zone_service_health_snapshot(&zone_kv, "zone.service.mail", "down").await;
        let storage =
            read_zone_service_health_snapshot(&zone_kv, "zone.service.storage", "unknown").await;
        let hypervisors = zone_kv
            .health_get("zone.service.hypervisor")
            .await
            .ok()
            .flatten()
            .and_then(|bytes| serde_json::from_slice::<HypervisorSnapshot>(&bytes).ok())
            .map(|snapshot| {
                snapshot
                    .nodes
                    .into_iter()
                    .map(|(node_code, cache)| zone_report_proto::HypervisorNode {
                        node_code,
                        status: cache.status,
                        cpu_cores_total: cache.cpu_cores_total,
                        cpu_cores_used: cache.cpu_cores_used,
                        ram_mb_total: cache.ram_mb_total,
                        ram_mb_used: cache.ram_mb_used,
                        storage_gb_total: cache.storage_gb_total,
                        storage_gb_used: cache.storage_gb_used,
                        updated_at: cache.updated_at,
                    })
                    .collect()
            })
            .unwrap_or_default();

        let report = zone_report_proto::ZoneReport {
            zone_id: config.zone_id.clone(),
            timestamp: now as i64,
            dataplane_cluster: Some(zone_report_proto::DataplaneCluster {
                active_nodes: alive_nodes as i64,
                avg_cpu_usage: if alive_nodes > 0 {
                    total_cpu / alive_nodes as f64
                } else {
                    0.0
                },
                avg_ram_usage: if alive_nodes > 0 {
                    total_ram / alive_nodes as f64
                } else {
                    0.0
                },
                total_active_workers: total_active_workers as i64,
                total_max_workers: (config.max_workers * alive_nodes.max(1)) as i64,
                job_queue_lag: total_job_queue_lag.min(i64::MAX as u64) as i64,
                job_queue_lag_stale,
            }),
            workloads: Some(zone_report_proto::Workloads {
                mail: Some(zone_report_proto::MailWorkload {
                    status: mail.status,
                    capacity: mail.capacity as i32,
                }),
                hypervisors,
                storage: Some(zone_report_proto::StorageWorkload {
                    status: storage.status,
                    capacity: storage.capacity as i32,
                }),
            }),
        };

        if session.permits_external_side_effect().await {
            if let Err(error) = kafka
                .publish_message(
                    &kafka.zone_report_topic(),
                    config.zone_id.as_bytes(),
                    &report,
                )
                .await
            {
                Logger::sys_warn(
                    "leader.zone_report_publisher",
                    "Không publish được Zone report",
                    &error,
                );
            }
        }

        if !session
            .wait(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000))
            .await
        {
            return;
        }
    }
}

async fn read_zone_service_health_snapshot(
    zone_kv: &ZoneKvStore,
    key: &str,
    default_status: &str,
) -> ServiceSnapshot {
    zone_kv
        .health_get(key)
        .await
        .ok()
        .flatten()
        .and_then(|bytes| serde_json::from_slice::<ServiceSnapshot>(&bytes).ok())
        .unwrap_or_else(|| ServiceSnapshot {
            status: default_status.to_string(),
            capacity: 0,
        })
}
