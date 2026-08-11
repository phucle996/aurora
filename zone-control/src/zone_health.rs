use bytes::Bytes;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::transfer_ticket::config::Config;
use crate::zone_control_kafka::ControlKafka;
use crate::zone_control_state::ZoneControlState;
use crate::zone_report_proto;

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
    #[serde(default)]
    loaded_payload_keys: Vec<NodePayloadKeyReadiness>,
}

#[derive(Deserialize)]
struct NodePayloadKeyReadiness {
    key_id: String,
    public_key_fingerprint: Vec<u8>,
}

#[derive(Deserialize)]
struct MailNodeSnapshot {
    node_id: String,
    observed_at_unix_ms: u64,
    pending_items: usize,
    in_flight_batches: usize,
    queue_capacity: usize,
}

#[derive(Deserialize)]
struct ServiceSnapshot {
    status: String,
    capacity: usize,
}

#[derive(Serialize)]
struct HealthSnapshot<'a> {
    status: &'a str,
    capacity: usize,
    updated_at: u64,
    probe_node_id: &'a str,
    fencing_token: u64,
}

#[derive(Serialize)]
struct MailHealthSnapshot {
    status: String,
    capacity: usize,
    pending_items: usize,
    in_flight_batches: usize,
    transport: &'static str,
    updated_at: u64,
    fencing_token: u64,
    probe_node_id: String,
}

#[derive(Deserialize, Serialize)]
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

pub(crate) async fn run_storage_probe(
    config: Config,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.storage_probe.0";
    let owner = format!("zone-control-storage-{assignment_epoch}");
    loop {
        let metadata = match state.read_metadata().await {
            Ok(metadata) => metadata,
            Err(error) => {
                tracing::warn!(event_code = "ZONE_CONTROL_STORAGE_METADATA_READ_FAILED", error = %error);
                wait_or_cancel(&shutdown, Duration::from_secs(1)).await;
                continue;
            }
        };
        let disabled = metadata.status == "disabled"
            || !metadata.services.get("storage").copied().unwrap_or(false);
        let (status, capacity) = if disabled {
            ("down", 0)
        } else if let (Some(host), Some(port)) = (&config.minio_host, config.minio_port) {
            match fetch_minio_liveness_http_response(host, port).await {
                Ok(response) if response.contains("200 OK") => ("healthy", 100),
                Ok(_) => ("degraded", 50),
                Err(error) => {
                    tracing::warn!(event_code = "ZONE_CONTROL_STORAGE_PROBE_FAILED", error = %error);
                    ("down", 0)
                }
            }
        } else {
            ("unknown", 0)
        };
        let value = serde_json::to_vec(&HealthSnapshot {
            status,
            capacity,
            updated_at: unix_time_seconds(),
            probe_node_id: &owner,
            fencing_token: assignment_epoch,
        })
        .map_err(|error| format!("encode storage health: {error}"))?;
        if !state
            .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        state
            .health_put_fenced("zone.service.storage", Bytes::from(value), assignment_epoch)
            .await?;
        if !wait_or_cancel(
            &shutdown,
            Duration::from_millis(4_500 + rand::random::<u64>() % 1_000),
        )
        .await
        {
            return Ok(());
        }
    }
}

pub(crate) async fn run_mail_probe(
    config: Config,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.mail_probe.0";
    let client = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(3))
        .timeout(Duration::from_secs(5))
        .build()
        .map_err(|error| format!("build mail health client: {error}"))?;
    loop {
        let jmap_reachable = if config.stalwart_jmap_url.is_empty() {
            false
        } else {
            let request = client.get(config.stalwart_jmap_url.trim());
            let request = if config.stalwart_reporter_bearer_token.is_empty() {
                request
            } else {
                request.bearer_auth(config.stalwart_reporter_bearer_token.trim())
            };
            request
                .send()
                .await
                .is_ok_and(|response| response.status().is_success())
        };
        let now_ms = unix_time_millis();
        let mut pending_items = 0_usize;
        let mut in_flight_batches = 0_usize;
        let mut queue_capacity = 0_usize;
        let mut nodes = 0_usize;
        for key in state.health_keys().await? {
            if !key.starts_with("mail.health.node.") {
                continue;
            }
            let Some(value) = state.health_get(&key).await? else {
                continue;
            };
            let Ok(snapshot) = serde_json::from_slice::<MailNodeSnapshot>(&value) else {
                continue;
            };
            if key.strip_prefix("mail.health.node.") != Some(snapshot.node_id.as_str())
                || now_ms.saturating_sub(snapshot.observed_at_unix_ms) > 30_000
            {
                continue;
            }
            nodes += 1;
            pending_items = pending_items.saturating_add(snapshot.pending_items);
            in_flight_batches = in_flight_batches.saturating_add(snapshot.in_flight_batches);
            queue_capacity = queue_capacity.saturating_add(snapshot.queue_capacity);
        }
        let (status, capacity) = if !jmap_reachable || nodes == 0 {
            ("down".to_string(), 0)
        } else if pending_items > queue_capacity.saturating_mul(8).max(1) {
            ("degraded".to_string(), 50)
        } else {
            ("healthy".to_string(), 100)
        };
        let snapshot = MailHealthSnapshot {
            status,
            capacity,
            pending_items,
            in_flight_batches,
            transport: "jmap_batch",
            updated_at: unix_time_seconds(),
            fencing_token: assignment_epoch,
            probe_node_id: format!("zone-control-mail-{assignment_epoch}"),
        };
        if !state
            .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        state
            .health_put_fenced(
                "zone.service.mail",
                Bytes::from(serde_json::to_vec(&snapshot).map_err(|error| error.to_string())?),
                assignment_epoch,
            )
            .await?;
        if !wait_or_cancel(
            &shutdown,
            Duration::from_millis(
                config.mail_health_observe_interval_ms + rand::random::<u64>() % 2_000,
            ),
        )
        .await
        {
            return Ok(());
        }
    }
}

pub(crate) async fn run_hypervisor_probe(
    config: Config,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.hypervisor_probe.0";
    let client = reqwest::Client::builder()
        .danger_accept_invalid_certs(config.proxmox_tls_insecure)
        .timeout(Duration::from_secs(5))
        .build()
        .map_err(|error| format!("build Proxmox health client: {error}"))?;
    loop {
        let (status, nodes) = if config.proxmox_api_url.is_empty()
            || config.proxmox_api_token.is_empty()
        {
            ("down".to_string(), HashMap::new())
        } else {
            let url = format!(
                "{}/api2/json/nodes",
                config.proxmox_api_url.trim_end_matches('/')
            );
            let response = client
                .get(url)
                .header("Authorization", config.proxmox_api_token.trim())
                .send()
                .await;
            match response {
                Ok(response) if response.status().is_success() => {
                    let payload = response
                        .json::<serde_json::Value>()
                        .await
                        .unwrap_or_default();
                    let mut nodes = HashMap::new();
                    for node in payload
                        .get("data")
                        .and_then(|value| value.as_array())
                        .into_iter()
                        .flatten()
                    {
                        let Some(name) = node.get("node").and_then(|value| value.as_str()) else {
                            continue;
                        };
                        let cpu = node
                            .get("cpu")
                            .and_then(|value| value.as_f64())
                            .unwrap_or_default();
                        let maxcpu = node
                            .get("maxcpu")
                            .and_then(|value| value.as_u64())
                            .unwrap_or_default();
                        let mem = node
                            .get("mem")
                            .and_then(|value| value.as_u64())
                            .unwrap_or_default();
                        let maxmem = node
                            .get("maxmem")
                            .and_then(|value| value.as_u64())
                            .unwrap_or_default();
                        nodes.insert(
                            name.to_string(),
                            HypervisorNodeCache {
                                status: if node.get("status").and_then(|value| value.as_str())
                                    == Some("online")
                                {
                                    "connected".to_string()
                                } else {
                                    "disconnected".to_string()
                                },
                                cpu_cores_total: maxcpu,
                                cpu_cores_used: (cpu * maxcpu as f64).round() as u64,
                                ram_mb_total: maxmem / 1_048_576,
                                ram_mb_used: mem / 1_048_576,
                                storage_gb_total: node
                                    .get("maxdisk")
                                    .and_then(|value| value.as_u64())
                                    .unwrap_or_default()
                                    / 1_073_741_824,
                                storage_gb_used: node
                                    .get("disk")
                                    .and_then(|value| value.as_u64())
                                    .unwrap_or_default()
                                    / 1_073_741_824,
                                updated_at: unix_time_seconds(),
                            },
                        );
                    }
                    let status = if nodes.values().any(|node| node.status == "connected") {
                        "healthy"
                    } else {
                        "down"
                    };
                    (status.to_string(), nodes)
                }
                _ => ("down".to_string(), HashMap::new()),
            }
        };
        let snapshot = HypervisorHealthSnapshot {
            status,
            nodes,
            updated_at: unix_time_seconds(),
            probe_node_id: format!("zone-control-hypervisor-{assignment_epoch}"),
            fencing_token: assignment_epoch,
        };
        if !state
            .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        state
            .health_put_fenced(
                "zone.service.hypervisor",
                Bytes::from(serde_json::to_vec(&snapshot).map_err(|error| error.to_string())?),
                assignment_epoch,
            )
            .await?;
        if !wait_or_cancel(
            &shutdown,
            Duration::from_millis(14_000 + rand::random::<u64>() % 2_000),
        )
        .await
        {
            return Ok(());
        }
    }
}

pub(crate) async fn run_zone_report(
    config: Config,
    state: Arc<ZoneControlState>,
    kafka: Arc<ControlKafka>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.zone_report.0";
    loop {
        let now = unix_time_seconds();
        let mut total_cpu = 0.0;
        let mut total_ram = 0.0;
        let mut total_active_workers = 0_usize;
        let mut total_queue_lag = 0_u64;
        let mut queue_lag_stale = false;
        let mut alive_nodes = 0_usize;
        let mut common_keys: Option<HashMap<Uuid, [u8; 32]>> = None;
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
            if now.saturating_sub(node.updated_at) > 15 {
                continue;
            }
            total_cpu += node.cpu;
            total_ram += node.ram;
            total_active_workers = total_active_workers.saturating_add(node.active_workers);
            total_queue_lag = total_queue_lag.saturating_add(node.job_queue_lag);
            queue_lag_stale |= node.job_queue_lag_stale;
            alive_nodes += 1;
            let mut node_keys = HashMap::new();
            for payload_key in node.loaded_payload_keys {
                let Ok(key_id) = Uuid::parse_str(&payload_key.key_id) else {
                    node_keys.clear();
                    break;
                };
                let Ok(fingerprint) = <[u8; 32]>::try_from(payload_key.public_key_fingerprint)
                else {
                    node_keys.clear();
                    break;
                };
                node_keys.insert(key_id, fingerprint);
            }
            match &mut common_keys {
                None => common_keys = Some(node_keys),
                Some(keys) => {
                    keys.retain(|key_id, fingerprint| node_keys.get(key_id) == Some(fingerprint))
                }
            }
        }
        if alive_nodes == 0 {
            queue_lag_stale = true;
        }
        let mail = read_service_snapshot(&state, "zone.service.mail", "down").await?;
        let storage = read_service_snapshot(&state, "zone.service.storage", "unknown").await?;
        let hypervisors = state
            .health_get("zone.service.hypervisor")
            .await?
            .and_then(|value| serde_json::from_slice::<HypervisorHealthSnapshot>(&value).ok())
            .map(|snapshot| {
                snapshot
                    .nodes
                    .into_iter()
                    .map(|(node_code, node)| zone_report_proto::HypervisorNode {
                        node_code,
                        status: node.status,
                        cpu_cores_total: node.cpu_cores_total as i64,
                        cpu_cores_used: node.cpu_cores_used as i64,
                        ram_mb_total: node.ram_mb_total as i64,
                        ram_mb_used: node.ram_mb_used as i64,
                        storage_gb_total: node.storage_gb_total as i64,
                        storage_gb_used: node.storage_gb_used as i64,
                        updated_at: node.updated_at as i64,
                    })
                    .collect()
            })
            .unwrap_or_default();
        let mut loaded_payload_keys = common_keys
            .unwrap_or_default()
            .into_iter()
            .map(
                |(key_id, fingerprint)| zone_report_proto::LoadedPayloadKey {
                    key_id: key_id.as_bytes().to_vec(),
                    public_key_fingerprint: fingerprint.to_vec(),
                },
            )
            .collect::<Vec<_>>();
        loaded_payload_keys.sort_unstable_by(|left, right| left.key_id.cmp(&right.key_id));
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
                total_max_workers: config.max_workers.saturating_mul(alive_nodes) as i64,
                job_queue_lag: total_queue_lag.min(i64::MAX as u64) as i64,
                job_queue_lag_stale: queue_lag_stale,
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
            loaded_payload_keys,
            leader_fencing_token: assignment_epoch,
        };
        if !state
            .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        kafka
            .publish_proto(
                &kafka.zone_report_topic(),
                config.zone_id.as_bytes(),
                &report,
            )
            .await?;
        if !wait_or_cancel(
            &shutdown,
            Duration::from_millis(4_500 + rand::random::<u64>() % 1_000),
        )
        .await
        {
            return Ok(());
        }
    }
}

async fn read_service_snapshot(
    state: &ZoneControlState,
    key: &str,
    default_status: &str,
) -> Result<ServiceSnapshot, String> {
    Ok(state
        .health_get(key)
        .await?
        .and_then(|value| serde_json::from_slice(&value).ok())
        .unwrap_or(ServiceSnapshot {
            status: default_status.to_string(),
            capacity: 0,
        }))
}

async fn fetch_minio_liveness_http_response(host: &str, port: u16) -> Result<String, String> {
    let mut stream = tokio::time::timeout(
        Duration::from_secs(2),
        tokio::net::TcpStream::connect(format!("{host}:{port}")),
    )
    .await
    .map_err(|_| "MinIO connect timeout".to_string())?
    .map_err(|error| error.to_string())?;
    stream
        .write_all(
            format!("GET /minio/health/live HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n")
                .as_bytes(),
        )
        .await
        .map_err(|error| error.to_string())?;
    let mut response = String::new();
    tokio::time::timeout(Duration::from_secs(2), stream.read_to_string(&mut response))
        .await
        .map_err(|_| "MinIO read timeout".to_string())?
        .map_err(|error| error.to_string())?;
    Ok(response)
}

fn default_true() -> bool {
    true
}
fn unix_time_seconds() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|value| value.as_secs())
        .unwrap_or_default()
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
