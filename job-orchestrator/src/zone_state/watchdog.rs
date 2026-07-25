use crate::config::Config;
use crate::observability::logger::Logger;
use std::time::Duration;

const LEASE_KEY: &str = "leader:{zone-health-watchdog}";
const LEASE_TTL_MS: u64 = 10_000;

/// Runs a cluster-wide dead-man switch under a short Shared Redis lease.
///
/// Kafka ownership is intentionally unrelated to watchdog ownership: reports
/// may be processed by any replica, while only one replica scans durable
/// observation timestamps. The SQL predicates fence a lease handoff, so an
/// expired leader can overlap safely with its successor.
pub async fn run(
    config: Config,
    redis_client: redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let pg_client =
        crate::infra::postgres::connect(&config.postgres, "zone_watchdog.postgres").await?;
    let mut redis = crate::infra::redis::multiplexed(&redis_client, &config.shared_redis).await?;
    let mut interval = tokio::time::interval(Duration::from_secs(5));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);

    loop {
        interval.tick().await;
        let token = uuid::Uuid::new_v4().to_string();
        let acquired: Option<String> = match redis::cmd("SET")
            .arg(LEASE_KEY)
            .arg(&token)
            .arg("NX")
            .arg("PX")
            .arg(LEASE_TTL_MS)
            .query_async(&mut redis)
            .await
        {
            Ok(value) => value,
            Err(error) => {
                Logger::sys_error(
                    "zone_watchdog.lease",
                    "Could not acquire Shared Redis watchdog lease",
                    &error.to_string(),
                );
                continue;
            }
        };
        if acquired.is_none() {
            continue;
        }

        let services = pg_client
            .execute(
                "UPDATE hierarchy.zone_services \
                 SET actual_state = 'down', updated_at = NOW() \
                 WHERE desired_state = TRUE \
                   AND actual_observed_at < NOW() - INTERVAL '30 seconds' \
                   AND actual_state::text != 'down'",
                &[],
            )
            .await;
        let nodes = pg_client
            .execute(
                "UPDATE hypervisor.nodes \
                 SET status = 'disconnected', updated_at = NOW() \
                 WHERE last_active_at < NOW() - INTERVAL '45 seconds' \
                   AND status != 'disconnected'",
                &[],
            )
            .await;

        match (services, nodes) {
            (Ok(service_count), Ok(node_count)) => {
                if service_count > 0 || node_count > 0 {
                    Logger::sys_warn(
                        "zone_watchdog.timeout",
                        &format!(
                            "Marked {service_count} Zone services down and {node_count} hypervisor nodes disconnected"
                        ),
                        "HEARTBEAT_TIMEOUT",
                    );
                }
            }
            (service_result, node_result) => {
                let error = service_result
                    .err()
                    .map(|value| value.to_string())
                    .or_else(|| node_result.err().map(|value| value.to_string()))
                    .unwrap_or_else(|| "unknown watchdog error".to_string());
                Logger::sys_error("zone_watchdog.scan", "Cluster watchdog scan failed", &error);
            }
        }

        let _: redis::RedisResult<i64> = redis::Script::new(
            "if redis.call('GET',KEYS[1]) == ARGV[1] then \
                 return redis.call('DEL',KEYS[1]) \
             end \
             return 0",
        )
        .key(LEASE_KEY)
        .arg(&token)
        .invoke_async(&mut redis)
        .await;
    }
}
