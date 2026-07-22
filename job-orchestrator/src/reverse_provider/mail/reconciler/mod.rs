mod consumer_tombstone;
mod personal_consumer;
mod personal_template;
mod scheduler;
mod tenant_consumer;
mod tenant_template;

pub use scheduler::run_periodic_mail_reconciliation;

use uuid::Uuid;

pub(super) const CONSUMER_EVENT_NAMESPACE: &str = "43de31a4-0c86-54e9-8384-47b33f541c28";
pub(super) const RECONCILE_COMPLETION_NAMESPACE: &str = "e295a8c6-c04f-56f3-9577-f53521006bb9";

// [COMMENT]: Đây là transport primitive duy nhất được dùng chung; business query/encode vẫn tách theo từng flow.
pub(super) async fn xadd_mail_projection_command(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    event_id: Uuid,
    job_topic: &str,
    resource_id: &str,
    payload: &[u8],
    generation: u64,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // [COMMENT]: Fencing check và XADD cùng Lua; owner cũ không enqueue sau khi lock đã sang generation mới.
    let stream_id: String = redis::Script::new(
        r#"
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return '' end
return redis.call('XADD', KEYS[2], '*',
  'job_id', ARGV[2], 'job_version', '1', 'attempt', '0',
  'job_topic', ARGV[3], 'source_domain', 'MAIL', 'resource_id', ARGV[4],
  'payload_schema_version', '1', 'payload', ARGV[5], 'trace_id', '',
  'idle', '60', 'reconcile_generation', ARGV[1])
"#,
    )
    .key(format!("mail:reconcile:lock:{zone_id}"))
    .key(format!("jobs:{zone_id}"))
    .arg(generation)
    .arg(event_id.to_string())
    .arg(job_topic)
    .arg(resource_id)
    .arg(payload)
    .invoke_async(redis_conn)
    .await?;
    if stream_id.is_empty() {
        return Err("MAIL_RECONCILE_FENCED".into());
    }
    Ok(())
}
