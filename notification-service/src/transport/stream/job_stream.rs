use crate::config::RuntimeConfig;
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
use crate::service::JobNotificationService;
use opentelemetry::trace::FutureExt;
use prost::Message;
use redis::streams::{
    StreamClaimReply, StreamId, StreamPendingCountReply, StreamReadOptions, StreamReadReply,
};
use redis::{AsyncCommands, Value};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;

pub const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
pub const JOB_NOTIFICATION_DLQ: &str = "stream:{job_notifications_quarantine}";
pub const JOB_NOTIFICATION_CONSUMER_GROUP: &str = "notification-job-v1";

pub mod proto {
    tonic::include_proto!("job");
}

pub use proto::JobNotificationEvent;

// [COMMENT]: Kiểm tra tính hợp lệ của sự kiện JobNotificationEvent từ Redis Stream
pub fn valid_event(event: &JobNotificationEvent) -> bool {
    (uuid::Uuid::parse_str(&event.notification_id).is_ok())
        && uuid::Uuid::parse_str(&event.job_id).is_ok()
        && uuid::Uuid::parse_str(&event.user_id).is_ok()
        && chrono::DateTime::from_timestamp(event.created_at, 0)
            .is_some_and(|value| value <= chrono::Utc::now() + chrono::Duration::minutes(5))
        && matches!(event.status.as_str(), "PROCESSING" | "SUCCESS" | "FAILED")
        && !event.event_type.is_empty()
        && event.event_type.len() <= 128
        && event.title.len() <= 256
        && event.message.len() <= 4_096
        && event.resource_id.len() <= 256
        && (event.event_type != "managed_service.instance.execute"
            || (event.status_version > 0
                && i64::try_from(event.status_version).is_ok()
                && ((event.status == "PROCESSING" && !event.status_version.is_multiple_of(2))
                    || (event.status != "PROCESSING" && event.status_version.is_multiple_of(2)))))
        && crate::observability::tracing::OtelTracer::is_valid_propagation_context(
            &event.trace_parent,
            &event.trace_state,
        )
}

pub fn parse_notification_id(event: &JobNotificationEvent) -> Result<String, uuid::Error> {
    uuid::Uuid::parse_str(&event.notification_id).map(|value| value.to_string())
}

// [COMMENT]: Consumer lắng nghe sự kiện Job Notification từ Redis Stream và chuyển giao cho JobNotificationService
pub struct JobStreamConsumer {
    client: Arc<redis::Client>,
    dispatcher: Arc<JobNotificationService>,
    config: RuntimeConfig,
    connect_timeout: Duration,
    shutdown: watch::Receiver<bool>,
    consumer_name: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum EntryOutcome {
    Delivered,
    Dropped,
}

impl JobStreamConsumer {
    pub fn new(
        client: Arc<redis::Client>,
        dispatcher: Arc<JobNotificationService>,
        config: RuntimeConfig,
        connect_timeout: Duration,
        shutdown: watch::Receiver<bool>,
    ) -> Self {
        let hostname =
            std::env::var("HOSTNAME").unwrap_or_else(|_| "notification-service".to_owned());
        Self {
            client,
            dispatcher,
            config,
            connect_timeout,
            shutdown,
            consumer_name: format!("{hostname}-{}", uuid::Uuid::new_v4().simple()),
        }
    }

    pub async fn run(mut self) {
        let mut retry_delay = self.config.reconnect_initial;
        loop {
            if *self.shutdown.borrow() {
                return;
            }
            match self.listen_loop().await {
                Ok(()) => retry_delay = self.config.reconnect_initial,
                Err(error) => {
                    Logger::sys_error(
                        "redis.job_stream",
                        "Shared Redis job stream consumer disconnected; reconnecting",
                        &error.to_string(),
                    );
                    if wait_for_shutdown(&mut self.shutdown, retry_delay).await {
                        return;
                    }
                    retry_delay =
                        std::cmp::min(retry_delay.saturating_mul(2), self.config.reconnect_max);
                }
            }
        }
    }

    async fn listen_loop(&mut self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let mut connection = tokio::time::timeout(
            self.connect_timeout,
            self.client.get_multiplexed_async_connection(),
        )
        .await??;
        ensure_consumer_group(&mut connection).await?;
        Logger::sys_info(
            "redis.job_stream",
            "Listening for job notifications on Shared Redis Stream",
        );

        loop {
            if *self.shutdown.borrow() {
                return Ok(());
            }
            let pending: StreamPendingCountReply = connection
                .xpending_count(
                    JOB_NOTIFICATION_STREAM,
                    JOB_NOTIFICATION_CONSUMER_GROUP,
                    "-",
                    "+",
                    self.config.stream_batch_size,
                )
                .await?;
            let entries = if pending.ids.is_empty() {
                read_new_entries(
                    &mut connection,
                    &self.consumer_name,
                    self.config.stream_batch_size,
                )
                .await?
            } else {
                let stale_ids = pending
                    .ids
                    .iter()
                    .filter(|entry| {
                        entry.last_delivered_ms
                            >= self.config.stream_claim_idle.as_millis() as usize
                    })
                    .map(|entry| entry.id.as_str())
                    .collect::<Vec<_>>();
                if stale_ids.is_empty() {
                    if wait_for_shutdown(&mut self.shutdown, Duration::from_millis(250)).await {
                        return Ok(());
                    }
                    continue;
                }
                let claimed: StreamClaimReply = connection
                    .xclaim(
                        JOB_NOTIFICATION_STREAM,
                        JOB_NOTIFICATION_CONSUMER_GROUP,
                        &self.consumer_name,
                        self.config.stream_claim_idle.as_millis() as usize,
                        &stale_ids,
                    )
                    .await?;
                claimed.ids
            };

            for entry in entries {
                if *self.shutdown.borrow() {
                    return Ok(());
                }
                if let Err(error) = self.process_entry(&mut connection, entry).await {
                    MetricsManager::record_redis_stream_event(
                        "job_notification",
                        "delivery_failed",
                    );
                    Logger::sys_error(
                        "redis.job_stream",
                        "Job notification remains pending for bounded retry",
                        &error.to_string(),
                    );
                    // Preserve per-stream ordering: a later terminal event must
                    // not overtake the first entry whose delivery failed.
                    break;
                }
            }
        }
    }

    async fn process_entry(
        &self,
        connection: &mut redis::aio::MultiplexedConnection,
        entry: StreamId,
    ) -> Result<EntryOutcome, Box<dyn std::error::Error + Send + Sync>> {
        let payload = match entry.map.get("payload") {
            Some(Value::BulkString(payload)) => payload,
            _ => {
                MetricsManager::record_redis_stream_event("job_notification", "invalid_envelope");
                quarantine_and_ack(
                    connection,
                    &entry.id,
                    "JOB_NOTIFICATION_ENVELOPE_INVALID",
                    0,
                )
                .await?;
                return Ok(EntryOutcome::Dropped);
            }
        };
        let event = match JobNotificationEvent::decode(payload.as_slice()) {
            Ok(event) if valid_event(&event) => event,
            _ => {
                MetricsManager::record_redis_stream_event("job_notification", "invalid_contract");
                Logger::sys_error(
                    "redis.job_stream",
                    "Dropped malformed internal job notification contract",
                    "JOB_NOTIFICATION_PROTO_INVALID",
                );
                quarantine_and_ack(
                    connection,
                    &entry.id,
                    "JOB_NOTIFICATION_PROTO_INVALID",
                    payload.len(),
                )
                .await?;
                return Ok(EntryOutcome::Dropped);
            }
        };

        let parent = OtelTracer::extract_context(&event.trace_parent, &event.trace_state);
        let context = OtelTracer::start_span_with_parent(
            format!("process {JOB_NOTIFICATION_STREAM}"),
            opentelemetry::trace::SpanKind::Consumer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "redis"),
                opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                opentelemetry::KeyValue::new("messaging.destination.name", JOB_NOTIFICATION_STREAM),
                opentelemetry::KeyValue::new("messaging.message.id", entry.id.clone()),
                opentelemetry::KeyValue::new("aurora.job.version", i64::from(event.job_version)),
                opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(event.attempt)),
            ],
            &parent,
        );

        let result = async {
            self.dispatcher.dispatch(event).await?;
            // Publish and ACK are intentionally separate systems. A crash
            // between them may duplicate a notification, so notification_id is
            // stable and consumers must remain idempotent.
            ack_and_delete(connection, &entry.id).await?;
            Ok::<EntryOutcome, Box<dyn std::error::Error + Send + Sync>>(EntryOutcome::Delivered)
        }
        .with_context(context.clone())
        .await;
        OtelTracer::finish_span(
            &context,
            result
                .as_ref()
                .err()
                .map(|_| "JOB_NOTIFICATION_DELIVERY_FAILED"),
        );
        if matches!(&result, Ok(EntryOutcome::Delivered)) {
            MetricsManager::record_redis_stream_event("job_notification", "delivered");
        }
        result
    }
}

async fn ensure_consumer_group(
    connection: &mut redis::aio::MultiplexedConnection,
) -> redis::RedisResult<()> {
    let result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(JOB_NOTIFICATION_STREAM)
        .arg(JOB_NOTIFICATION_CONSUMER_GROUP)
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
    connection: &mut redis::aio::MultiplexedConnection,
    consumer_name: &str,
    batch_size: usize,
) -> redis::RedisResult<Vec<StreamId>> {
    let options = StreamReadOptions::default()
        .group(JOB_NOTIFICATION_CONSUMER_GROUP, consumer_name)
        .count(batch_size)
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
    connection: &mut redis::aio::MultiplexedConnection,
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
    .arg(JOB_NOTIFICATION_CONSUMER_GROUP)
    .arg(entry_id)
    .invoke_async(connection)
    .await?;
    Ok(())
}

async fn quarantine_and_ack(
    connection: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
    reason: &str,
    payload_len: usize,
) -> redis::RedisResult<()> {
    let _: i64 = redis::Script::new(
        r#"
        redis.call('XADD', KEYS[2], 'MAXLEN', '~', 10000, '*',
          'source_entry_id', ARGV[2], 'reason', ARGV[3], 'payload_len', ARGV[4])
        local acknowledged = redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
        if acknowledged == 1 then
            return redis.call('XDEL', KEYS[1], ARGV[2])
        end
        return 0
        "#,
    )
    .key(JOB_NOTIFICATION_STREAM)
    .key(JOB_NOTIFICATION_DLQ)
    .arg(JOB_NOTIFICATION_CONSUMER_GROUP)
    .arg(entry_id)
    .arg(reason)
    .arg(payload_len)
    .invoke_async(connection)
    .await?;
    Ok(())
}

async fn wait_for_shutdown(shutdown: &mut watch::Receiver<bool>, delay: Duration) -> bool {
    tokio::select! {
        changed = shutdown.changed() => changed.is_ok() && *shutdown.borrow(),
        _ = tokio::time::sleep(delay) => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn event() -> proto::JobNotificationEvent {
        proto::JobNotificationEvent {
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
            notification_id: uuid::Uuid::new_v4().to_string(),
            status_version: 0,
        }
    }

    #[test]
    fn rolling_event_without_trace_context_is_valid() {
        assert!(valid_event(&event()));
    }

    #[test]
    fn missing_notification_id_is_rejected() {
        let mut candidate = event();
        candidate.notification_id.clear();
        assert!(!valid_event(&candidate));
        assert!(parse_notification_id(&candidate).is_err());
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

    #[test]
    fn status_timestamp_and_identity_are_strictly_validated() {
        let mut candidate = event();
        candidate.status = "SUCCEEDED".to_string();
        assert!(!valid_event(&candidate));

        let mut candidate = event();
        candidate.user_id = "not-a-uuid".to_string();
        assert!(!valid_event(&candidate));

        let mut candidate = event();
        candidate.created_at = (chrono::Utc::now() + chrono::Duration::minutes(6)).timestamp();
        assert!(!valid_event(&candidate));
    }

    #[test]
    fn managed_service_status_version_parity_fences_processing_and_terminal_updates() {
        let mut candidate = event();
        candidate.event_type = "managed_service.instance.execute".to_string();
        candidate.status = "PROCESSING".to_string();
        candidate.status_version = 3;
        assert!(valid_event(&candidate));

        candidate.status_version = 4;
        assert!(!valid_event(&candidate));

        candidate.status = "SUCCESS".to_string();
        assert!(valid_event(&candidate));

        candidate.status_version = 3;
        assert!(!valid_event(&candidate));

        candidate.status_version = u64::MAX;
        assert!(!valid_event(&candidate));
    }

    #[tokio::test]
    #[ignore = "requires a disposable Redis from NOTIFICATION_TEST_REDIS_URL"]
    async fn quarantine_is_metadata_only_and_atomically_settles_source_entry() {
        let url = std::env::var("NOTIFICATION_TEST_REDIS_URL")
            .expect("NOTIFICATION_TEST_REDIS_URL must point to disposable Redis");
        let client = redis::Client::open(url).expect("Redis client");
        let mut connection = client
            .get_multiplexed_async_connection()
            .await
            .expect("Redis connection");
        redis::cmd("FLUSHDB")
            .query_async::<()>(&mut connection)
            .await
            .expect("flush disposable DB");
        ensure_consumer_group(&mut connection)
            .await
            .expect("consumer group");
        let source_payload = b"raw-customer-secret-must-not-enter-quarantine";
        redis::cmd("XADD")
            .arg(JOB_NOTIFICATION_STREAM)
            .arg("*")
            .arg("payload")
            .arg(source_payload.as_slice())
            .query_async::<String>(&mut connection)
            .await
            .expect("stream append");
        let entries = read_new_entries(&mut connection, "integration-test", 1)
            .await
            .expect("read into PEL");
        assert_eq!(entries.len(), 1);

        quarantine_and_ack(
            &mut connection,
            &entries[0].id,
            "JOB_NOTIFICATION_PROTO_INVALID",
            source_payload.len(),
        )
        .await
        .expect("quarantine and ACK");

        let source_len: usize = connection
            .xlen(JOB_NOTIFICATION_STREAM)
            .await
            .expect("source length");
        let quarantine_len: usize = connection
            .xlen(JOB_NOTIFICATION_DLQ)
            .await
            .expect("quarantine length");
        let quarantine: Value = redis::cmd("XRANGE")
            .arg(JOB_NOTIFICATION_DLQ)
            .arg("-")
            .arg("+")
            .query_async(&mut connection)
            .await
            .expect("quarantine rows");
        let quarantine_debug = format!("{quarantine:?}");
        assert_eq!(source_len, 0);
        assert_eq!(quarantine_len, 1);
        assert!(quarantine_debug.contains("JOB_NOTIFICATION_PROTO_INVALID"));
        assert!(!quarantine_debug.contains("raw-customer-secret"));
    }
}
