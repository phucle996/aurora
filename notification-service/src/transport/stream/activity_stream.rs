use crate::config::RuntimeConfig;
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
use crate::repo::{ActivityCategory, ActivityEvent, ActivityOutcome, ActorType};
use crate::service::ports::AppError;
use crate::service::ActivityService;
use chrono::DateTime;
use opentelemetry::trace::FutureExt;
use prost::Message;
use redis::streams::{
    StreamClaimReply, StreamId, StreamPendingCountReply, StreamReadOptions, StreamReadReply,
};
use redis::{AsyncCommands, Value};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;
use uuid::Uuid;

pub const USER_ACTIVITY_STREAM: &str = "stream:{user_activity}";
pub const USER_ACTIVITY_DLQ: &str = "stream:{user_activity_quarantine}";
pub const USER_ACTIVITY_CONSUMER_GROUP: &str = "notification-activity-v1";

pub mod proto {
    tonic::include_proto!("activity");
}

// [COMMENT]: Giải mã UserActivityEvent từ Protobuf sang domain ActivityEvent với kiểm tra tính hợp lệ
pub fn decode_activity_event(event: proto::UserActivityEvent) -> Result<ActivityEvent, AppError> {
    let actor_id = optional_uuid(&event.actor_id, "activity actor id")?;
    let occurred_at = DateTime::from_timestamp(event.occurred_at, 0)
        .ok_or_else(|| invalid("activity timestamp is invalid"))?;
    if occurred_at > chrono::Utc::now() + chrono::Duration::minutes(5) {
        return Err(invalid("activity timestamp is too far in the future"));
    }
    if event.resource_type.len() > 128
        || event.resource_id.len() > 256
        || event.operation_id.len() > 256
    {
        return Err(invalid("activity routing metadata is too large"));
    }
    if !OtelTracer::is_valid_propagation_context(&event.trace_parent, &event.trace_state) {
        return Err(invalid("activity trace context is invalid"));
    }

    let activity = ActivityEvent {
        event_id: Uuid::parse_str(&event.event_id)
            .map_err(|_| invalid("activity event id is not a UUID"))?,
        user_id: Uuid::parse_str(&event.user_id)
            .map_err(|_| invalid("activity user id is not a UUID"))?,
        category: ActivityCategory::parse(&event.category)
            .ok_or_else(|| invalid("activity category is invalid"))?,
        action: event.action,
        actor_type: ActorType::parse(&event.actor_type)
            .ok_or_else(|| invalid("activity actor type is invalid"))?,
        actor_id,
        outcome: ActivityOutcome::parse(&event.outcome)
            .ok_or_else(|| invalid("activity outcome is invalid"))?,
        source_service: event.source_service,
        resource_type: optional_text(event.resource_type),
        resource_id: optional_text(event.resource_id),
        operation_id: optional_text(event.operation_id),
        title: event.title,
        summary: event.summary,
        occurred_at,
        metadata_json: event.metadata_json,
        schema_version: event.schema_version,
        projection_version: 0,
    };
    activity.validate()?;
    Ok(activity)
}

fn optional_uuid(value: &str, field: &str) -> Result<Option<Uuid>, AppError> {
    if value.is_empty() {
        return Ok(None);
    }
    Uuid::parse_str(value)
        .map(Some)
        .map_err(|_| invalid(&format!("{field} is not a UUID")))
}

fn optional_text(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}

// [COMMENT]: Consumer lắng nghe sự kiện User Activity từ Redis Stream
pub struct ActivityStreamConsumer {
    client: Arc<redis::Client>,
    activities: Arc<ActivityService>,
    config: RuntimeConfig,
    connect_timeout: Duration,
    shutdown: watch::Receiver<bool>,
    consumer_name: String,
}

impl ActivityStreamConsumer {
    pub fn new(
        client: Arc<redis::Client>,
        activities: Arc<ActivityService>,
        config: RuntimeConfig,
        connect_timeout: Duration,
        shutdown: watch::Receiver<bool>,
    ) -> Self {
        let hostname =
            std::env::var("HOSTNAME").unwrap_or_else(|_| "notification-service".to_owned());
        Self {
            client,
            activities,
            config,
            connect_timeout,
            shutdown,
            consumer_name: format!("{hostname}-activity-{}", uuid::Uuid::new_v4().simple()),
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
                        "redis.activity_stream",
                        "Shared Redis activity stream consumer disconnected; reconnecting",
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
            "redis.activity_stream",
            "Listening for durable user activity on Shared Redis Stream",
        );

        loop {
            if *self.shutdown.borrow() {
                return Ok(());
            }
            let pending: StreamPendingCountReply = connection
                .xpending_count(
                    USER_ACTIVITY_STREAM,
                    USER_ACTIVITY_CONSUMER_GROUP,
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
                        USER_ACTIVITY_STREAM,
                        USER_ACTIVITY_CONSUMER_GROUP,
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
                    MetricsManager::record_redis_stream_event("user_activity", "delivery_failed");
                    Logger::sys_error(
                        "redis.activity_stream",
                        "User activity remains pending for bounded retry",
                        &error.to_string(),
                    );
                    // A lower stream ID must settle before this consumer reads
                    // later entries, preserving source ordering per producer.
                    break;
                }
            }
        }
    }

    async fn process_entry(
        &self,
        connection: &mut redis::aio::MultiplexedConnection,
        entry: StreamId,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let payload = match entry.map.get("payload") {
            Some(Value::BulkString(payload)) => payload,
            _ => {
                quarantine_and_ack(connection, &entry.id, "ACTIVITY_ENVELOPE_INVALID", 0).await?;
                MetricsManager::record_redis_stream_event("user_activity", "invalid_envelope");
                return Ok(());
            }
        };
        let wire_event = match proto::UserActivityEvent::decode(payload.as_slice()) {
            Ok(event) => event,
            Err(_) => {
                quarantine_and_ack(
                    connection,
                    &entry.id,
                    "ACTIVITY_PROTO_INVALID",
                    payload.len(),
                )
                .await?;
                MetricsManager::record_redis_stream_event("user_activity", "invalid_contract");
                return Ok(());
            }
        };
        let parent = OtelTracer::extract_context(&wire_event.trace_parent, &wire_event.trace_state);
        let event = match decode_activity_event(wire_event) {
            Ok(event) => event,
            Err(error) => {
                quarantine_and_ack(
                    connection,
                    &entry.id,
                    "ACTIVITY_CONTRACT_INVALID",
                    payload.len(),
                )
                .await?;
                MetricsManager::record_redis_stream_event("user_activity", "invalid_contract");
                Logger::sys_warn(
                    "redis.activity_quarantined",
                    "Quarantined invalid user activity without copying its payload",
                    &error.to_string(),
                );
                return Ok(());
            }
        };
        let context = OtelTracer::start_span_with_parent(
            format!("process {USER_ACTIVITY_STREAM}"),
            opentelemetry::trace::SpanKind::Consumer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "redis"),
                opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                opentelemetry::KeyValue::new("messaging.destination.name", USER_ACTIVITY_STREAM),
                opentelemetry::KeyValue::new("messaging.message.id", entry.id.clone()),
            ],
            &parent,
        );

        let result = async {
            self.activities.persist(event).await?;
            ack_and_delete(connection, &entry.id).await?;
            Ok::<(), Box<dyn std::error::Error + Send + Sync>>(())
        }
        .with_context(context.clone())
        .await;
        OtelTracer::finish_span(
            &context,
            result.as_ref().err().map(|_| "ACTIVITY_PERSIST_FAILED"),
        );
        if result.is_ok() {
            MetricsManager::record_redis_stream_event("user_activity", "delivered");
        }
        result
    }
}

async fn ensure_consumer_group(
    connection: &mut redis::aio::MultiplexedConnection,
) -> redis::RedisResult<()> {
    let result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(USER_ACTIVITY_STREAM)
        .arg(USER_ACTIVITY_CONSUMER_GROUP)
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
        .group(USER_ACTIVITY_CONSUMER_GROUP, consumer_name)
        .count(batch_size)
        .block(2_000);
    let reply: StreamReadReply = connection
        .xread_options(&[USER_ACTIVITY_STREAM], &[">"], &options)
        .await?;
    Ok(reply
        .keys
        .into_iter()
        .flat_map(|stream| stream.ids)
        .collect())
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
    .key(USER_ACTIVITY_STREAM)
    .key(USER_ACTIVITY_DLQ)
    .arg(USER_ACTIVITY_CONSUMER_GROUP)
    .arg(entry_id)
    .arg(reason)
    .arg(payload_len)
    .invoke_async(connection)
    .await?;
    Ok(())
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
    .key(USER_ACTIVITY_STREAM)
    .arg(USER_ACTIVITY_CONSUMER_GROUP)
    .arg(entry_id)
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

    fn wire_event() -> proto::UserActivityEvent {
        let user_id = Uuid::new_v4();
        proto::UserActivityEvent {
            event_id: Uuid::new_v4().to_string(),
            user_id: user_id.to_string(),
            category: "security".to_string(),
            action: "session.login".to_string(),
            actor_type: "self".to_string(),
            actor_id: user_id.to_string(),
            outcome: "succeeded".to_string(),
            source_service: "acr".to_string(),
            resource_type: String::new(),
            resource_id: String::new(),
            operation_id: Uuid::new_v4().to_string(),
            title: "Signed in".to_string(),
            summary: "A new session was created".to_string(),
            occurred_at: chrono::Utc::now().timestamp(),
            metadata_json: "{}".to_string(),
            schema_version: 1,
            trace_parent: String::new(),
            trace_state: String::new(),
        }
    }

    #[test]
    fn valid_contract_maps_to_timeline_event() {
        let event = wire_event();
        let expected_user = Uuid::parse_str(&event.user_id).expect("user UUID");
        let decoded = decode_activity_event(event).expect("decode activity");
        assert_eq!(decoded.user_id, expected_user);
        assert_eq!(decoded.category, ActivityCategory::Security);
        assert_eq!(decoded.outcome, ActivityOutcome::Succeeded);
    }

    #[test]
    fn invalid_identity_enum_and_future_timestamp_are_rejected() {
        let mut event = wire_event();
        event.user_id = "not-a-uuid".to_string();
        assert!(decode_activity_event(event).is_err());

        let mut event = wire_event();
        event.category = "superuser".to_string();
        assert!(decode_activity_event(event).is_err());

        let mut event = wire_event();
        event.occurred_at = (chrono::Utc::now() + chrono::Duration::minutes(6)).timestamp();
        assert!(decode_activity_event(event).is_err());
    }

    #[test]
    fn bounded_metadata_and_trace_contract_are_enforced() {
        let mut event = wire_event();
        event.resource_id = "x".repeat(257);
        assert!(decode_activity_event(event).is_err());

        let mut event = wire_event();
        event.metadata_json = format!(r#"{{"value":"{}"}}"#, "x".repeat(16 * 1024));
        assert!(decode_activity_event(event).is_err());

        let mut event = wire_event();
        event.trace_state = "vendor=value".to_string();
        assert!(decode_activity_event(event).is_err());
    }

    #[tokio::test]
    #[ignore = "requires a disposable Redis from NOTIFICATION_TEST_ACTIVITY_REDIS_URL"]
    async fn successful_activity_ack_removes_the_durable_source_entry() {
        let url = std::env::var("NOTIFICATION_TEST_ACTIVITY_REDIS_URL")
            .expect("NOTIFICATION_TEST_ACTIVITY_REDIS_URL must point to disposable Redis");
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
        redis::cmd("XADD")
            .arg(USER_ACTIVITY_STREAM)
            .arg("*")
            .arg("payload")
            .arg(b"durable-activity".as_slice())
            .query_async::<String>(&mut connection)
            .await
            .expect("stream append");
        let entries = read_new_entries(&mut connection, "integration-test", 1)
            .await
            .expect("read into PEL");
        assert_eq!(entries.len(), 1);

        ack_and_delete(&mut connection, &entries[0].id)
            .await
            .expect("ACK and delete");

        let source_len: usize = connection
            .xlen(USER_ACTIVITY_STREAM)
            .await
            .expect("source length");
        let pending: StreamPendingCountReply = connection
            .xpending_count(
                USER_ACTIVITY_STREAM,
                USER_ACTIVITY_CONSUMER_GROUP,
                "-",
                "+",
                10,
            )
            .await
            .expect("pending list");
        assert_eq!(source_len, 0);
        assert!(pending.ids.is_empty());
    }
}
