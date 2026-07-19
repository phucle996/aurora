use super::MailRuntime;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub struct MailWorkloadMonitor;

impl MailWorkloadMonitor {
    pub fn start(
        config: Arc<Config>,
        redis_internal_zone: Arc<RedisClientManager>,
        runtime: Arc<MailRuntime>,
    ) {
        tokio::spawn(async move {
            loop {
                let (zone_status, mail_enabled) = load_zone_state(&redis_internal_zone)
                    .await
                    .unwrap_or_else(|_| ("active".to_string(), "enabled".to_string()));
                let disabled = zone_status == "disabled" || mail_enabled == "disabled";
                let pending = runtime.stats.pending_items.load(Ordering::Relaxed);
                let in_flight = runtime.stats.in_flight_batches.load(Ordering::Relaxed);
                let queue_capacity = config.mail_batch_queue_capacity.max(1);
                let queue_ratio = (pending as f64 / queue_capacity as f64).min(1.0);

                let healthy = if disabled {
                    false
                } else {
                    tokio::time::timeout(Duration::from_secs(3), runtime.healthcheck())
                        .await
                        .map(|result| result.is_ok())
                        .unwrap_or(false)
                };
                let (status, capacity) = if disabled || !healthy {
                    ("down", 0usize)
                } else {
                    let capacity = ((1.0 - queue_ratio) * 100.0) as usize;
                    (if capacity < 10 { "degraded" } else { "healthy" }, capacity)
                };

                if let Err(error) =
                    publish_status(&redis_internal_zone, status, capacity, pending, in_flight).await
                {
                    Logger::sys_warn(
                        "mail_monitor.redis",
                        "Failed to publish JMAP mail workload status",
                        &error,
                    );
                }
                tokio::time::sleep(Duration::from_secs(5)).await;
            }
        });
    }
}

async fn load_zone_state(redis: &RedisClientManager) -> Result<(String, String), String> {
    let mut conn = redis
        .client()
        .get_multiplexed_async_connection()
        .await
        .map_err(|error| error.to_string())?;
    let metadata: std::collections::HashMap<String, String> = redis::cmd("HGETALL")
        .arg("infra:zone:metadata")
        .query_async(&mut conn)
        .await
        .map_err(|error| error.to_string())?;
    Ok((
        metadata
            .get("status")
            .cloned()
            .unwrap_or_else(|| "active".to_string()),
        metadata
            .get("service:mail")
            .cloned()
            .unwrap_or_else(|| "enabled".to_string()),
    ))
}

async fn publish_status(
    redis: &RedisClientManager,
    status: &str,
    capacity: usize,
    pending: usize,
    in_flight: usize,
) -> Result<(), String> {
    let mut conn = redis
        .client()
        .get_multiplexed_async_connection()
        .await
        .map_err(|error| error.to_string())?;
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or_default();
    redis::cmd("HSET")
        .arg("infra:mail")
        .arg("status")
        .arg(status)
        .arg("capacity")
        .arg(capacity)
        .arg("pending_items")
        .arg(pending)
        .arg("in_flight_batches")
        .arg(in_flight)
        .arg("transport")
        .arg("jmap_batch")
        .arg("updated_at")
        .arg(now)
        .query_async::<_, ()>(&mut conn)
        .await
        .map_err(|error| error.to_string())
}
