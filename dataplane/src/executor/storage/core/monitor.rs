use bytes::Bytes;
use serde::Serialize;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

#[derive(Serialize)]
struct StorageHealthSnapshot<'a> {
    status: &'a str,
    capacity: usize,
    updated_at: u64,
    probe_node_id: &'a str,
    fencing_token: u64,
}

pub struct StorageWorkloadMonitor;

impl StorageWorkloadMonitor {
    pub fn start(config: Arc<Config>, zone_kv: Arc<ZoneKvStore>) {
        tokio::spawn(async move {
            let instance_id = std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
            loop {
                let lease = match zone_kv
                    .acquire_rotating_lease(
                        "lease.health.storage",
                        &instance_id,
                        Duration::from_secs(5),
                        Duration::from_secs(6),
                    )
                    .await
                {
                    Ok(Some(lease)) => lease,
                    Ok(None) => {
                        tokio::time::sleep(Duration::from_secs(5)).await;
                        continue;
                    }
                    Err(error) => {
                        Logger::sys_warn(
                            "storage_monitor.nats_kv",
                            "Không thể lấy storage health lease",
                            &error,
                        );
                        tokio::time::sleep(Duration::from_secs(2)).await;
                        continue;
                    }
                };

                let metadata = zone_kv.read_zone_metadata().await.unwrap_or_default();
                let disabled = metadata.status == "disabled"
                    || !metadata.services.get("storage").copied().unwrap_or(true);
                let (status, capacity) = if disabled {
                    ("down", 0)
                } else {
                    match (&config.minio_host, config.minio_port) {
                        (Some(host), Some(port)) => {
                            let tcp_ok = tokio::time::timeout(
                                Duration::from_secs(2),
                                tokio::net::TcpStream::connect(format!("{host}:{port}")),
                            )
                            .await
                            .is_ok_and(|result| result.is_ok());
                            if !tcp_ok {
                                ("down", 0)
                            } else if Self::fetch_liveness_raw(host, port)
                                .await
                                .is_ok_and(|response| response.contains("200 OK"))
                            {
                                ("healthy", 100)
                            } else {
                                ("degraded", 50)
                            }
                        }
                        _ => ("unknown", 0),
                    }
                };
                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|duration| duration.as_secs())
                    .unwrap_or_default();
                let snapshot = StorageHealthSnapshot {
                    status,
                    capacity,
                    updated_at: now,
                    probe_node_id: &instance_id,
                    fencing_token: lease.fencing_token,
                };
                if let Ok(value) = serde_json::to_vec(&snapshot) {
                    // [COMMENT]: Snapshot chỉ được ghi khi lease còn hiệu lực và token không lùi.
                    if zone_kv
                        .renew_lease(&lease, Duration::from_secs(5))
                        .await
                        .unwrap_or(false)
                    {
                        if let Err(error) = zone_kv
                            .health_put_fenced(
                                "zone.service.storage",
                                Bytes::from(value),
                                lease.fencing_token,
                            )
                            .await
                        {
                            Logger::sys_warn(
                                "storage_monitor.nats_kv",
                                "Không thể ghi storage health snapshot",
                                &error,
                            );
                        }
                    }
                }
                let _ = zone_kv.release_lease(&lease).await;
                tokio::time::sleep(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000))
                    .await;
            }
        });
    }

    async fn fetch_liveness_raw(host: &str, port: u16) -> Result<String, String> {
        let mut stream = tokio::time::timeout(
            Duration::from_secs(2),
            tokio::net::TcpStream::connect(format!("{host}:{port}")),
        )
        .await
        .map_err(|_| "Connect timeout".to_string())?
        .map_err(|error| error.to_string())?;
        stream
            .write_all(
                format!(
                    "GET /minio/health/live HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n"
                )
                .as_bytes(),
            )
            .await
            .map_err(|error| error.to_string())?;
        let mut response = String::new();
        tokio::time::timeout(Duration::from_secs(2), stream.read_to_string(&mut response))
            .await
            .map_err(|_| "Read timeout".to_string())?
            .map_err(|error| error.to_string())?;
        Ok(response)
    }
}
