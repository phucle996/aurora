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
    let topic = kafka.zone_command_topic(&zone_id.to_string());
    let producer_context = crate::observability::otel::OtelTracer::start_current_span(
        format!("send {topic}"),
        opentelemetry::trace::SpanKind::Producer,
        vec![
            opentelemetry::KeyValue::new("messaging.system", "kafka"),
            opentelemetry::KeyValue::new("messaging.operation.type", "send"),
            opentelemetry::KeyValue::new("messaging.destination.name", topic.clone()),
            opentelemetry::KeyValue::new("aurora.job.id", event_id.to_string()),
            opentelemetry::KeyValue::new("aurora.job.topic", job_topic.to_string()),
            opentelemetry::KeyValue::new("aurora.zone.id", zone_id.to_string()),
            opentelemetry::KeyValue::new("aurora.reconcile.generation", generation as i64),
        ],
    );
    let propagation = crate::observability::otel::OtelTracer::inject_context(&producer_context);
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
        traceparent: propagation.traceparent,
        tracestate: propagation.tracestate,
    };
    use opentelemetry::trace::FutureExt;
    let publish_result = kafka
        .publish_message(&topic, event_id.as_bytes(), &command)
        .with_context(producer_context.clone())
        .await;
    crate::observability::otel::OtelTracer::finish_span(
        &producer_context,
        publish_result
            .as_ref()
            .err()
            .map(|_| "KAFKA_RECONCILE_PUBLISH_FAILED"),
    );
    publish_result.map_err(std::io::Error::other)?;
    Ok(())
}
