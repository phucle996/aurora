mod consumer_tombstone;
mod personal_consumer;
mod personal_template;
mod scheduler;
mod tenant_consumer;
mod tenant_template;

pub use scheduler::run_periodic_mail_reconciliation;

use crate::infra::kafka::transport_proto::JobCommandV1;
use crate::infra::kafka::KafkaTransport;
use uuid::Uuid;

pub(super) const CONSUMER_EVENT_NAMESPACE: &str = "43de31a4-0c86-54e9-8384-47b33f541c28";
pub(super) const RECONCILE_COMPLETION_NAMESPACE: &str = "e295a8c6-c04f-56f3-9577-f53521006bb9";

// [COMMENT]: Đây là transport primitive duy nhất được dùng chung; business query/encode vẫn tách theo từng flow.
pub(super) async fn publish_mail_projection_command(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    kafka: &KafkaTransport,
    zone_id: Uuid,
    event_id: Uuid,
    job_topic: &str,
    resource_id: &str,
    payload: &[u8],
    generation: u64,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // [COMMENT]: Redis token fence trước publish; generation trong envelope tiếp tục fence stale owner tại Zone.
    let owns_lock: i64 = redis::Script::new(
        "if redis.call('GET', KEYS[1]) == ARGV[1] then return 1 else return 0 end",
    )
    .key(format!("mail:reconcile:lock:{zone_id}"))
    .arg(generation)
    .invoke_async(redis_conn)
    .await?;
    if owns_lock != 1 {
        return Err("MAIL_RECONCILE_FENCED".into());
    }
    let command = JobCommandV1 {
        job_id: event_id.as_bytes().to_vec(),
        job_version: 1,
        attempt: 0,
        job_topic: job_topic.to_string(),
        source_domain: "MAIL".to_string(),
        resource_id: resource_id.to_string(),
        payload_schema_version: 1,
        payload: payload.to_vec(),
        trace_id: Vec::new(),
        idle_seconds: Some(60),
        reconcile_generation: Some(generation),
        target_zone_id: zone_id.to_string(),
        transport_schema_version: 1,
    };
    kafka
        .publish_message(
            &kafka.zone_command_topic(&zone_id.to_string()),
            event_id.as_bytes(),
            &command,
        )
        .await
        .map_err(std::io::Error::other)?;
    Ok(())
}
