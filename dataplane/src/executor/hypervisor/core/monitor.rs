use bytes::Bytes;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::client::ProxmoxClient;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

pub struct HypervisorMonitor;

#[derive(Clone, Deserialize, Serialize)]
struct HypervisorNodeCache {
    status: String,
    cpu_cores_total: u64,
    cpu_cores_used: u64,
    ram_mb_total: u64,
    ram_mb_used: u64,
    storage_gb_total: u64,
    storage_gb_used: u64,
    updated_at: u64,
}

#[derive(Deserialize, Serialize)]
struct HypervisorHealthSnapshot {
    status: String,
    nodes: HashMap<String, HypervisorNodeCache>,
    updated_at: u64,
    probe_node_id: String,
    fencing_token: u64,
}

impl HypervisorMonitor {
    pub fn start(config: Arc<Config>, zone_kv: Arc<ZoneKvStore>) {
        if config.proxmox_api_url.is_empty() || config.proxmox_api_token.is_empty() {
            Logger::sys_warn(
                "hypervisor_monitor.start",
                "PROXMOX_API_URL hoặc PROXMOX_API_TOKEN chưa được cấu hình.",
                "Hypervisor workload sẽ không được báo cáo.",
            );
            return;
        }
        tokio::spawn(async move {
            let instance_id = std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
            let client = ProxmoxClient::new(&config);
            loop {
                let lease = match zone_kv
                    .acquire_rotating_lease(
                        "lease.health.hypervisor",
                        &instance_id,
                        Duration::from_secs(15),
                        Duration::from_secs(20),
                    )
                    .await
                {
                    Ok(Some(lease)) => lease,
                    Ok(None) => {
                        tokio::time::sleep(Duration::from_secs(15)).await;
                        continue;
                    }
                    Err(error) => {
                        Logger::sys_warn(
                            "hypervisor_monitor.nats_kv",
                            "Không thể lấy hypervisor health lease",
                            &error,
                        );
                        tokio::time::sleep(Duration::from_secs(5)).await;
                        continue;
                    }
                };
                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|duration| duration.as_secs())
                    .unwrap_or_default();
                let metadata = zone_kv.read_zone_metadata().await.unwrap_or_default();
                let disabled = metadata.status == "disabled"
                    || !metadata.services.get("hypervisor").copied().unwrap_or(true);
                if disabled {
                    let snapshot = HypervisorHealthSnapshot {
                        status: "down".to_string(),
                        nodes: HashMap::new(),
                        updated_at: now,
                        probe_node_id: instance_id.clone(),
                        fencing_token: lease.fencing_token,
                    };
                    if let Ok(value) = serde_json::to_vec(&snapshot) {
                        if zone_kv
                            .renew_lease(&lease, Duration::from_secs(15))
                            .await
                            .unwrap_or(false)
                        {
                            let _ = zone_kv
                                .health_put_fenced(
                                    "zone.service.hypervisor",
                                    Bytes::from(value),
                                    lease.fencing_token,
                                )
                                .await;
                        }
                    }
                    let _ = zone_kv.release_lease(&lease).await;
                    tokio::time::sleep(Duration::from_millis(
                        14_000 + rand::random::<u64>() % 2_000,
                    ))
                    .await;
                    continue;
                }
                let nodes = match client.fetch_nodes().await {
                    Ok(nodes) => nodes
                        .iter()
                        .map(|node| {
                            let cache = HypervisorNodeCache {
                                status: Self::compute_node_status(node),
                                cpu_cores_total: node.maxcpu,
                                cpu_cores_used: (node.cpu * node.maxcpu as f64).round() as u64,
                                ram_mb_total: node.maxmem / 1_048_576,
                                ram_mb_used: node.mem / 1_048_576,
                                storage_gb_total: node.maxdisk / 1_073_741_824,
                                storage_gb_used: node.disk / 1_073_741_824,
                                updated_at: now,
                            };
                            (node.node.clone(), cache)
                        })
                        .collect(),
                    Err(error) => {
                        Logger::sys_error(
                            "hypervisor_monitor.poll_fail",
                            "Không thể kết nối Proxmox API",
                            &error,
                        );
                        let mut previous = zone_kv
                            .health_get("zone.service.hypervisor")
                            .await
                            .ok()
                            .flatten()
                            .and_then(|bytes| {
                                serde_json::from_slice::<HypervisorHealthSnapshot>(&bytes).ok()
                            })
                            .map(|snapshot| snapshot.nodes)
                            .unwrap_or_default();
                        for node in previous.values_mut() {
                            node.status = "disconnected".to_string();
                            node.updated_at = now;
                        }
                        previous
                    }
                };
                let snapshot = HypervisorHealthSnapshot {
                    status: if nodes.values().any(|node| node.status == "connected") {
                        "healthy".to_string()
                    } else {
                        "down".to_string()
                    },
                    nodes,
                    updated_at: now,
                    probe_node_id: instance_id.clone(),
                    fencing_token: lease.fencing_token,
                };
                if let Ok(value) = serde_json::to_vec(&snapshot) {
                    // [COMMENT]: Proxmox poll có thể dài hơn TTL; chỉ current owner với token cao nhất được publish snapshot.
                    if zone_kv
                        .renew_lease(&lease, Duration::from_secs(15))
                        .await
                        .unwrap_or(false)
                    {
                        let _ = zone_kv
                            .health_put_fenced(
                                "zone.service.hypervisor",
                                Bytes::from(value),
                                lease.fencing_token,
                            )
                            .await;
                    }
                }
                let _ = zone_kv.release_lease(&lease).await;
                tokio::time::sleep(Duration::from_millis(
                    14_000 + rand::random::<u64>() % 2_000,
                ))
                .await;
            }
        });
    }

    fn compute_node_status(node: &super::client::ProxmoxNodeRaw) -> String {
        if node.status != "online" {
            return "disconnected".to_string();
        }
        let ram_pct = if node.maxmem > 0 {
            node.mem as f64 / node.maxmem as f64 * 100.0
        } else {
            0.0
        };
        if node.cpu * 100.0 > 90.0 || ram_pct > 90.0 {
            "degraded".to_string()
        } else {
            "connected".to_string()
        }
    }
}
