use super::MailRuntime;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use bytes::Bytes;
use serde::Serialize;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

#[derive(Serialize)]
struct MailHealthSnapshot<'a> {
    status: &'a str,
    capacity: usize,
    pending_items: usize,
    in_flight_batches: usize,
    transport: &'static str,
    updated_at: u64,
    fencing_token: u64,
    probe_node_id: &'a str,
}

pub struct MailWorkloadMonitor;

impl MailWorkloadMonitor {
    pub fn start(config: Arc<Config>, zone_kv: Arc<ZoneKvStore>, runtime: Arc<MailRuntime>) {
        tokio::spawn(async move {
            let instance_id = std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
            loop {
                let lease = match zone_kv
                    .acquire_rotating_lease(
                        "lease.health.mail",
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
                            "mail_monitor.nats_kv",
                            "Failed to acquire mail health lease",
                            &error,
                        );
                        tokio::time::sleep(Duration::from_secs(2)).await;
                        continue;
                    }
                };
                let metadata = zone_kv.read_zone_metadata().await.unwrap_or_default();
                let disabled = metadata.status == "disabled"
                    || !metadata.services.get("mail").copied().unwrap_or(true);
                let pending = runtime.stats.pending_items.load(Ordering::Relaxed);
                let in_flight = runtime.stats.in_flight_batches.load(Ordering::Relaxed);
                let queue_ratio =
                    (pending as f64 / config.mail_batch_queue_capacity.max(1) as f64).min(1.0);
                let healthy = !disabled
                    && tokio::time::timeout(Duration::from_secs(3), runtime.healthcheck())
                        .await
                        .is_ok_and(|result| result.is_ok());
                let (status, capacity) = if !healthy {
                    ("down", 0)
                } else {
                    let capacity = ((1.0 - queue_ratio) * 100.0) as usize;
                    (if capacity < 10 { "degraded" } else { "healthy" }, capacity)
                };
                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|duration| duration.as_secs())
                    .unwrap_or_default();
                let snapshot = MailHealthSnapshot {
                    status,
                    capacity,
                    pending_items: pending,
                    in_flight_batches: in_flight,
                    transport: "jmap_batch",
                    updated_at: now,
                    fencing_token: lease.fencing_token,
                    probe_node_id: &instance_id,
                };
                if let Ok(value) = serde_json::to_vec(&snapshot) {
                    // [COMMENT]: Renew sát side effect và CAS theo fencing token để probe cũ không ghi đè cycle mới.
                    if zone_kv
                        .renew_lease(&lease, Duration::from_secs(5))
                        .await
                        .unwrap_or(false)
                    {
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
                tokio::time::sleep(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000))
                    .await;
            }
        });
    }
}
