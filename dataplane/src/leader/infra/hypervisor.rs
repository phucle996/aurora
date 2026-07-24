use bytes::Bytes;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::super::leadership::ZoneLeaderSession;
use crate::config::Config;
use crate::executor::hypervisor::core::client::{ProxmoxClient, ProxmoxNodeRaw};
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};

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

pub(crate) async fn run_hypervisor_health_probe(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
) {
    if config.proxmox_api_url.is_empty() || config.proxmox_api_token.is_empty() {
        Logger::sys_warn(
            "leader.hypervisor_health_probe",
            "PROXMOX_API_URL hoặc PROXMOX_API_TOKEN chưa được cấu hình",
            "Hypervisor workload sẽ không được báo cáo",
        );
        let cancel = session.cancellation_token();
        cancel.cancelled().await;
        return;
    }
    let client = ProxmoxClient::new(&config);

    loop {
        let now = now_seconds();
        let metadata = match zone_kv.read_zone_metadata().await {
            Ok(metadata) => metadata,
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.hypervisor_health_probe",
                    "HYPERVISOR_ZONE_METADATA_READ_FAILED",
                    "Cannot determine whether hypervisor probing is enabled; skipping probe fail-closed",
                    &error,
                    LogFields {
                        leader_fencing_token: Some(session.fencing_token()),
                        retryable: Some(true),
                        outcome: Some("skipped"),
                        ..LogFields::default()
                    },
                );
                if !session.wait(Duration::from_secs(2)).await {
                    return;
                }
                continue;
            }
        };
        let disabled = metadata.status == "disabled"
            || !metadata
                .services
                .get("hypervisor")
                .copied()
                .unwrap_or(false);
        let nodes = if disabled {
            HashMap::new()
        } else {
            // [COMMENT]: Chỉ current leader được mở request tới Proxmox; KV partition fail-closed.
            if !session.permits_external_side_effect().await {
                return;
            }
            let cancel = session.cancellation_token();
            match tokio::select! {
                _ = cancel.cancelled() => return,
                result = client.fetch_nodes() => result,
            } {
                Ok(nodes) => nodes
                    .iter()
                    .map(|node| {
                        (
                            node.node.clone(),
                            HypervisorNodeCache {
                                status: compute_hypervisor_node_health_status(node),
                                cpu_cores_total: node.maxcpu,
                                cpu_cores_used: (node.cpu * node.maxcpu as f64).round() as u64,
                                ram_mb_total: node.maxmem / 1_048_576,
                                ram_mb_used: node.mem / 1_048_576,
                                storage_gb_total: node.maxdisk / 1_073_741_824,
                                storage_gb_used: node.disk / 1_073_741_824,
                                updated_at: now,
                            },
                        )
                    })
                    .collect(),
                Err(error) => {
                    Logger::sys_error(
                        "leader.hypervisor_health_probe",
                        "Không thể kết nối Proxmox API",
                        &error,
                    );
                    load_previous_hypervisor_nodes_as_disconnected(&zone_kv, now).await
                }
            }
        };

        let snapshot = HypervisorHealthSnapshot {
            status: if disabled {
                "down".to_string()
            } else if nodes.values().any(|node| node.status == "connected") {
                "healthy".to_string()
            } else {
                "down".to_string()
            },
            nodes,
            updated_at: now,
            probe_node_id: session.owner_id().to_string(),
            fencing_token: session.fencing_token(),
        };
        if session.permits_external_side_effect().await {
            match serde_json::to_vec(&snapshot) {
                Ok(value) => {
                    if let Err(error) = zone_kv
                        .health_put_fenced(
                            "zone.service.hypervisor",
                            Bytes::from(value),
                            session.fencing_token(),
                        )
                        .await
                    {
                        Logger::sys_warn_with_fields(
                            "leader.hypervisor_health_probe",
                            "HYPERVISOR_ZONE_HEALTH_WRITE_FAILED",
                            "Could not write fenced Zone hypervisor health snapshot",
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
                    "leader.hypervisor_health_probe",
                    "HYPERVISOR_ZONE_HEALTH_SERIALIZE_FAILED",
                    "Could not serialize Zone hypervisor health snapshot",
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
                14_000 + rand::random::<u64>() % 2_000,
            ))
            .await
        {
            return;
        }
    }
}

async fn load_previous_hypervisor_nodes_as_disconnected(
    zone_kv: &ZoneKvStore,
    now: u64,
) -> HashMap<String, HypervisorNodeCache> {
    let mut previous = zone_kv
        .health_get("zone.service.hypervisor")
        .await
        .ok()
        .flatten()
        .and_then(|bytes| serde_json::from_slice::<HypervisorHealthSnapshot>(&bytes).ok())
        .map(|snapshot| snapshot.nodes)
        .unwrap_or_default();
    for node in previous.values_mut() {
        node.status = "disconnected".to_string();
        node.updated_at = now;
    }
    previous
}

fn compute_hypervisor_node_health_status(node: &ProxmoxNodeRaw) -> String {
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

fn now_seconds() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or_default()
}
