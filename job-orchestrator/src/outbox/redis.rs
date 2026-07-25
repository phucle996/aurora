use crate::config::{OwnershipWorkflowConfig, SharedRedisConfig};
use redis::aio::ConnectionManager;
use std::sync::Arc;
use tokio::sync::Mutex;

pub const OWNERSHIP_STREAM: &str = "stream:{billing}:resource_ownership";

/// Serializes XADD and WAITAOF on one Redis connection. WAITAOF only fences
/// writes issued on that connection, so these two commands must not be split
/// across a pool checkout or a reconnecting clone.
pub struct SharedStreamPublisher {
    connection: Mutex<ConnectionManager>,
    stream_capacity: usize,
    replica_acks: i64,
    wait_timeout_ms: u64,
}

impl SharedStreamPublisher {
    pub async fn connect(
        client: &redis::Client,
        redis_config: &SharedRedisConfig,
        ownership_config: &OwnershipWorkflowConfig,
    ) -> Result<Arc<Self>, redis::RedisError> {
        let connection = crate::infra::redis::manager(client, redis_config).await?;
        Ok(Arc::new(Self {
            connection: Mutex::new(connection),
            stream_capacity: ownership_config.stream_capacity,
            replica_acks: redis_config.aof_replica_acks,
            wait_timeout_ms: redis_config.aof_timeout_ms,
        }))
    }

    pub async fn publish_ownership(
        &self,
        event_id: &str,
        event_type: &str,
        payload: &[u8],
    ) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
        let mut connection = self.connection.lock().await;
        // Capacity check and XADD are atomic across JO replicas. When Cost is
        // unavailable, PostgreSQL retains the pending intent instead of letting
        // a Central soft-state dependency consume Redis memory without bound.
        let stream_id: String = redis::Script::new(
            r#"
            if redis.call('XLEN', KEYS[1]) >= tonumber(ARGV[1]) then
                return redis.error_reply('OWNERSHIP_STREAM_CAPACITY_REACHED')
            end
            return redis.call(
                'XADD', KEYS[1], '*',
                'event_id', ARGV[2],
                'event_type', ARGV[3],
                'payload', ARGV[4]
            )
            "#,
        )
        .key(OWNERSHIP_STREAM)
        .arg(self.stream_capacity)
        .arg(event_id)
        .arg(event_type)
        .arg(payload)
        .invoke_async(&mut *connection)
        .await?;

        let (local_aof, replica_aof): (i64, i64) = redis::cmd("WAITAOF")
            .arg(1)
            .arg(self.replica_acks)
            .arg(self.wait_timeout_ms)
            .query_async(&mut *connection)
            .await?;
        if local_aof < 1 || replica_aof < self.replica_acks {
            // XADD may already be durable. The PostgreSQL marker remains pending
            // and retry can duplicate; Cost inbox event_id/hash makes that safe.
            return Err(format!(
                "Shared Redis durability fence not met: local={local_aof}, replicas={replica_aof}, required={}",
                self.replica_acks
            )
            .into());
        }

        Ok(stream_id)
    }
}
