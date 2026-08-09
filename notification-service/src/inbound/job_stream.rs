use crate::application::job_notifications::JobNotificationService;
use crate::config::RuntimeConfig;
use crate::contract::job::{
    proto::JobNotificationEvent, valid_event, JOB_NOTIFICATION_CONSUMER_GROUP,
    JOB_NOTIFICATION_DLQ, JOB_NOTIFICATION_STREAM,
};
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
use opentelemetry::trace::FutureExt;
use prost::Message;
use redis::streams::{
    StreamClaimReply, StreamId, StreamPendingCountReply, StreamReadOptions, StreamReadReply,
};
use redis::{AsyncCommands, Value};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;

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
    // Quarantine metadata is sufficient for operations and avoids copying a
    // malformed payload that could contain a secret or unbounded customer data.
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
