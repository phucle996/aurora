use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;
use crate::observability::metrics::MetricsManager;
use opentelemetry::trace::FutureExt;
use prost::Message;
use redis::streams::{
    StreamClaimReply, StreamId, StreamPendingCountReply, StreamReadOptions, StreamReadReply,
};
use redis::{AsyncCommands, Value};
use std::sync::Arc;
use std::time::Duration;

const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
const CONSUMER_GROUP: &str = "notification-service-v1";
const READ_BATCH_SIZE: usize = 16;
const CLAIM_IDLE_MS: usize = 30_000;

pub mod job {
    tonic::include_proto!("job");
}

pub struct RedisSubscriber {
    client: Arc<redis::Client>,
    centrifugo_client: CentrifugoClient,
    consumer_name: String,
}

impl RedisSubscriber {
    pub fn new(client: Arc<redis::Client>, centrifugo_client: CentrifugoClient) -> Self {
        let hostname =
            std::env::var("HOSTNAME").unwrap_or_else(|_| "notification-service".to_string());
        Self {
            client,
            centrifugo_client,
            // A new process identity lets XCLAIM distinguish a restarted pod from
            // the dead connection that still owns entries in the PEL.
            consumer_name: format!("{hostname}-{}", uuid::Uuid::new_v4().simple()),
        }
    }

    pub async fn start_listening(&self) {
        let mut retry_delay = Duration::from_secs(1);
        loop {
            match self.listen_loop().await {
                Ok(()) => retry_delay = Duration::from_secs(1),
                Err(error) => {
                    Logger::sys_error(
                        "redis.job_notifications",
                        "Shared Redis notification consumer disconnected; reconnecting",
                        &error.to_string(),
                    );
                    tokio::time::sleep(retry_delay).await;
                    retry_delay = std::cmp::min(retry_delay * 2, Duration::from_secs(30));
                }
            }
        }
    }

    async fn listen_loop(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let mut connection = self.client.get_async_connection().await?;
        ensure_consumer_group(&mut connection).await?;
        Logger::sys_info(
            "redis.job_notifications",
            "Listening for job notifications on Shared Redis Stream",
        );

        loop {
            let pending: StreamPendingCountReply = connection
                .xpending_count(
                    JOB_NOTIFICATION_STREAM,
                    CONSUMER_GROUP,
                    "-",
                    "+",
                    READ_BATCH_SIZE,
                )
                .await?;
            let entries = if pending.ids.is_empty() {
                read_new_entries(&mut connection, &self.consumer_name).await?
            } else {
                let stale_ids = pending
                    .ids
                    .iter()
                    .filter(|entry| entry.last_delivered_ms >= CLAIM_IDLE_MS)
                    .map(|entry| entry.id.as_str())
                    .collect::<Vec<_>>();
                if stale_ids.is_empty() {
                    // Do not overtake an in-flight lower entry. This globally bounds
                    // concurrency and preserves notification order across replicas.
                    tokio::time::sleep(Duration::from_millis(250)).await;
                    continue;
                }
                let claimed: StreamClaimReply = connection
                    .xclaim(
                        JOB_NOTIFICATION_STREAM,
                        CONSUMER_GROUP,
                        &self.consumer_name,
                        CLAIM_IDLE_MS,
                        &stale_ids,
                    )
                    .await?;
                claimed.ids
            };

            for entry in entries {
                if let Err(error) = self.process_entry(&mut connection, entry).await {
                    MetricsManager::record_redis_stream_event("delivery_failed");
                    Logger::sys_error(
                        "redis.job_notifications",
                        "Job notification remains pending for bounded retry",
                        &error.to_string(),
                    );
                    // Entries were delivered to this consumer as an ordered batch.
                    // Stop here so later terminal state cannot overtake this entry.
                    break;
                }
            }
        }
    }

    async fn process_entry(
        &self,
        connection: &mut redis::aio::Connection,
        entry: StreamId,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let payload = match entry.map.get("data") {
            Some(Value::Data(payload)) => payload,
            _ => {
                MetricsManager::record_redis_stream_event("invalid_envelope");
                Logger::sys_error(
                    "redis.job_notifications",
                    "Dropping malformed internal notification envelope",
                    "REDIS_NOTIFICATION_DATA_REQUIRED",
                );
                ack_and_delete(connection, &entry.id).await?;
                return Ok(());
            }
        };
        let event = match job::JobNotificationEvent::decode(payload.as_slice()) {
            Ok(event) if valid_event(&event) => event,
            _ => {
                MetricsManager::record_redis_stream_event("invalid_contract");
                Logger::sys_error(
                    "redis.job_notifications",
                    "Dropping malformed internal job notification contract",
                    "JOB_NOTIFICATION_PROTO_INVALID",
                );
                ack_and_delete(connection, &entry.id).await?;
                return Ok(());
            }
        };

        let parent = crate::observability::otel::OtelTracer::extract_context(
            &event.trace_parent,
            &event.trace_state,
        );
        let context = crate::observability::otel::OtelTracer::start_span_with_parent(
            format!("process {}", JOB_NOTIFICATION_STREAM),
            opentelemetry::trace::SpanKind::Consumer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "redis"),
                opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                opentelemetry::KeyValue::new("messaging.destination.name", JOB_NOTIFICATION_STREAM),
                opentelemetry::KeyValue::new("messaging.message.id", entry.id.clone()),
                opentelemetry::KeyValue::new("aurora.job.id", event.job_id.clone()),
                opentelemetry::KeyValue::new("aurora.job.version", i64::from(event.job_version)),
                opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(event.attempt)),
            ],
            &parent,
        );

        let result: Result<(), Box<dyn std::error::Error + Send + Sync>> = async {
            let created_at = chrono::DateTime::from_timestamp(event.created_at, 0)
                .ok_or("invalid notification timestamp")?
                .to_rfc3339();
            let client_payload = serde_json::json!({
                "status": event.status,
                "title": event.title,
                "message": event.message,
                "created_at": created_at,
                "job_id": event.job_id,
                "operation": event.event_type,
                "resource_id": event.resource_id,
                "job_version": event.job_version,
                "attempt": event.attempt,
            });
            crate::service::job::notification::handle_job_notification(
                &self.centrifugo_client,
                &event.user_id,
                client_payload,
            )
            .await?;
            // ACK and XDEL are atomic. A crash after Centrifugo publish but before
            // this script leaves the entry in PEL and may duplicate transaction_id.
            ack_and_delete(connection, &entry.id).await?;
            Ok(())
        }
        .with_context(context.clone())
        .await;

        crate::observability::otel::OtelTracer::finish_span(
            &context,
            result
                .as_ref()
                .err()
                .map(|_| "JOB_NOTIFICATION_DELIVERY_FAILED"),
        );
        if result.is_ok() {
            MetricsManager::record_redis_stream_event("delivered");
        }
        result
    }
}

async fn ensure_consumer_group(connection: &mut redis::aio::Connection) -> redis::RedisResult<()> {
    let result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(JOB_NOTIFICATION_STREAM)
        .arg(CONSUMER_GROUP)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(connection)
        .await;
    match result {
        Ok(()) => Ok(()),
        Err(error) if error.code() == Some("BUSYGROUP") => Ok(()),
        Err(error) => Err(error),
    }
}

async fn read_new_entries(
    connection: &mut redis::aio::Connection,
    consumer_name: &str,
) -> redis::RedisResult<Vec<StreamId>> {
    let options = StreamReadOptions::default()
        .group(CONSUMER_GROUP, consumer_name)
        .count(READ_BATCH_SIZE)
        .block(2_000);
    let reply: StreamReadReply = connection
        .xread_options(&[JOB_NOTIFICATION_STREAM], &[">"], &options)
        .await?;
    Ok(reply
        .keys
        .into_iter()
        .flat_map(|stream| stream.ids)
        .collect())
}

async fn ack_and_delete(
    connection: &mut redis::aio::Connection,
    entry_id: &str,
) -> redis::RedisResult<()> {
    let _: i64 = redis::Script::new(
        r#"
        local acknowledged = redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
        if acknowledged == 1 then
            return redis.call('XDEL', KEYS[1], ARGV[2])
        end
        return 0
        "#,
    )
    .key(JOB_NOTIFICATION_STREAM)
    .arg(CONSUMER_GROUP)
    .arg(entry_id)
    .invoke_async(connection)
    .await?;
    Ok(())
}

fn valid_event(event: &job::JobNotificationEvent) -> bool {
    uuid::Uuid::parse_str(&event.job_id).is_ok()
        && uuid::Uuid::parse_str(&event.user_id).is_ok()
        && matches!(event.status.as_str(), "PROCESSING" | "SUCCESS" | "FAILED")
        && !event.event_type.is_empty()
        && event.event_type.len() <= 128
        && event.title.len() <= 256
        && event.message.len() <= 4_096
        && event.resource_id.len() <= 256
        && crate::observability::otel::OtelTracer::is_valid_propagation_context(
            &event.trace_parent,
            &event.trace_state,
        )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn event() -> job::JobNotificationEvent {
        job::JobNotificationEvent {
            job_id: uuid::Uuid::new_v4().to_string(),
            user_id: uuid::Uuid::new_v4().to_string(),
            status: "SUCCESS".to_string(),
            event_type: "storage.bucket.create".to_string(),
            title: "Bucket Created".to_string(),
            message: String::new(),
            created_at: chrono::Utc::now().timestamp(),
            trace_parent: String::new(),
            trace_state: String::new(),
            resource_id: "bucket-1".to_string(),
            job_version: 1,
            attempt: 0,
        }
    }

    #[test]
    fn rolling_event_without_trace_context_is_valid() {
        assert!(valid_event(&event()));
    }

    #[test]
    fn partial_trace_context_and_oversized_message_are_rejected() {
        let mut candidate = event();
        candidate.trace_state = "vendor=value".to_string();
        assert!(!valid_event(&candidate));

        candidate.trace_state.clear();
        candidate.message = "x".repeat(4_097);
        assert!(!valid_event(&candidate));
    }
}
