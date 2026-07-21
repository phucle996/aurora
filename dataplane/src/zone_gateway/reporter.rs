use prost::Message;
use serde::Deserialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::reconciler;
use super::zone_proto;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

#[derive(Deserialize)]
struct NodeSnapshot {
    cpu: f64,
    ram: f64,
    active_workers: usize,
    updated_at: u64,
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

/// [COMMENT]: Chỉ lease holder tổng hợp snapshot Zone; replica khác không cùng XADD một chu kỳ.
pub fn start_zone_gateway(
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
    config: Arc<Config>,
) {
    tokio::spawn(async move {
        let instance_id = std::env::var("HOSTNAME")
            .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
        let mut counter = 720_u64;
        loop {
            if counter >= 720 {
                counter = 0;
                let zone_kv = zone_kv.clone();
                let redis_job = redis_job.clone();
                let config = config.clone();
                tokio::spawn(async move {
                    if let Err(error) =
                        reconciler::sync_zone_metadata(zone_kv, redis_job, config).await
                    {
                        Logger::sys_error(
                            "zone_gateway.sync_metadata",
                            "Đồng bộ Zone metadata thất bại",
                            &error.to_string(),
                        );
                    }
                });
            }

            let lease = match zone_kv
                .acquire_rotating_lease(
                    "lease.gateway.report",
                    &instance_id,
                    Duration::from_secs(15),
                    Duration::from_secs(6),
                )
                .await
            {
                Ok(Some(lease)) => lease,
                Ok(None) => {
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    counter += 1;
                    continue;
                }
                Err(error) => {
                    Logger::sys_warn("zone_gateway.nats_kv", "Không thể lấy report lease", &error);
                    tokio::time::sleep(Duration::from_secs(2)).await;
                    continue;
                }
            };

            let now = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_secs())
                .unwrap_or_default();
            let mut total_cpu = 0.0;
            let mut total_ram = 0.0;
            let mut total_active_workers = 0_usize;
            let mut alive_nodes = 0_usize;
            for key in zone_kv.health_keys().await.unwrap_or_default() {
                if !key.starts_with("zone.node.") {
                    continue;
                }
                let Some(bytes) = zone_kv.health_get(key).await.ok().flatten() else {
                    continue;
                };
                let Ok(node) = serde_json::from_slice::<NodeSnapshot>(&bytes) else {
                    continue;
                };
                if now.saturating_sub(node.updated_at) <= 15 {
                    total_cpu += node.cpu;
                    total_ram += node.ram;
                    total_active_workers += node.active_workers;
                    alive_nodes += 1;
                }
            }
            let mail = zone_kv
                .health_get("zone.service.mail")
                .await
                .ok()
                .flatten()
                .and_then(|bytes| serde_json::from_slice::<ServiceSnapshot>(&bytes).ok())
                .unwrap_or(ServiceSnapshot {
                    status: "down".to_string(),
                    capacity: 0,
                });
            let storage = zone_kv
                .health_get("zone.service.storage")
                .await
                .ok()
                .flatten()
                .and_then(|bytes| serde_json::from_slice::<ServiceSnapshot>(&bytes).ok())
                .unwrap_or(ServiceSnapshot {
                    status: "unknown".to_string(),
                    capacity: 0,
                });
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
                        .map(|(node_code, cache)| zone_proto::HypervisorNode {
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

            let report = zone_proto::ZoneReport {
                zone_id: config.zone_id.clone(),
                timestamp: now as i64,
                dataplane_cluster: Some(zone_proto::DataplaneCluster {
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
                }),
                workloads: Some(zone_proto::Workloads {
                    mail: Some(zone_proto::MailWorkload {
                        status: mail.status,
                        capacity: mail.capacity as i32,
                    }),
                    hypervisors,
                    storage: Some(zone_proto::StorageWorkload {
                        status: storage.status,
                        capacity: storage.capacity as i32,
                    }),
                }),
            };
            let mut payload = Vec::new();
            // [COMMENT]: Aggregate cũ hết lease không được XADD sau khi reporter mới đã takeover.
            let may_publish = zone_kv
                .renew_lease(&lease, Duration::from_secs(15))
                .await
                .unwrap_or(false);
            if may_publish && report.encode(&mut payload).is_ok() {
                if let Ok(mut connection) =
                    redis_job.client().get_multiplexed_tokio_connection().await
                {
                    let _: redis::RedisResult<()> = redis::cmd("XADD")
                        .arg("zone:backpressure:reports")
                        .arg("MAXLEN")
                        .arg("~")
                        .arg("1000")
                        .arg("*")
                        .arg("zone_id")
                        .arg(&config.zone_id)
                        .arg("payload")
                        .arg(payload)
                        .query_async(&mut connection)
                        .await;
                }
            }
            let _ = zone_kv.release_lease(&lease).await;
            tokio::time::sleep(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000)).await;
            counter += 1;
        }
    });
}
