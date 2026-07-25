use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use serde::Deserialize;
use std::sync::Arc;
use std::time::Duration;

const REALTIME_CHANNEL: &str = "aurora:realtime:notifications";
const MAX_ENVELOPE_BYTES: usize = 256 * 1024;

#[derive(Deserialize)]
struct RealtimeEnvelope {
    kind: String,
    user_id: String,
    payload: serde_json::Value,
}

/// Central-local soft-state bridge. A disconnect can lose wake-ups, while
/// authoritative APIs and the mail runtime TTL snapshot provide recovery.
pub struct RealtimeListener {
    redis: Arc<redis::Client>,
    centrifugo: CentrifugoClient,
}

impl RealtimeListener {
    pub fn new(redis: Arc<redis::Client>, centrifugo: CentrifugoClient) -> Self {
        Self { redis, centrifugo }
    }

    pub async fn start_listening(&self) {
        let mut delay = Duration::from_secs(1);
        loop {
            match self.listen_once().await {
                Ok(()) => delay = Duration::from_secs(1),
                Err(error) => {
                    Logger::sys_error(
                        "redis.realtime",
                        "Shared Redis realtime subscriber stopped; reconnecting",
                        &error.to_string(),
                    );
                    tokio::time::sleep(delay).await;
                    delay = std::cmp::min(delay * 2, Duration::from_secs(30));
                }
            }
        }
    }

    async fn listen_once(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let connection = self.redis.get_async_connection().await?;
        let mut subscriber = connection.into_pubsub();
        subscriber.subscribe(REALTIME_CHANNEL).await?;
        Logger::sys_info(
            "redis.realtime",
            "Listening for Central realtime notifications on Shared Redis",
        );

        let mut messages = subscriber.on_message();
        while let Some(message) = messages.next().await {
            let raw = message.get_payload_bytes();
            if raw.len() > MAX_ENVELOPE_BYTES {
                crate::observability::metrics::MetricsManager::record_redis_realtime(
                    "unknown",
                    "oversized",
                );
                Logger::sys_warn(
                    "redis.realtime",
                    "Dropping oversized realtime notification",
                    "REALTIME_ENVELOPE_TOO_LARGE",
                );
                continue;
            }
            let envelope: RealtimeEnvelope = match serde_json::from_slice::<RealtimeEnvelope>(raw) {
                Ok(value)
                    if uuid::Uuid::parse_str(&value.user_id).is_ok()
                        && matches!(value.kind.as_str(), "storage" | "mail_runtime") =>
                {
                    value
                }
                _ => {
                    crate::observability::metrics::MetricsManager::record_redis_realtime(
                        "unknown", "invalid",
                    );
                    Logger::sys_warn(
                        "redis.realtime",
                        "Dropping invalid realtime notification envelope",
                        "REALTIME_ENVELOPE_INVALID",
                    );
                    continue;
                }
            };

            // Process sequentially to bound memory during a Centrifugo outage.
            let result = match envelope.kind.as_str() {
                "storage" => {
                    crate::service::storage::bucket::handle_bucket_size_sync(
                        &self.centrifugo,
                        &envelope.user_id,
                        envelope.payload,
                    )
                    .await
                }
                "mail_runtime" => {
                    crate::service::mail::runtime::handle_consumer_runtime(
                        &self.centrifugo,
                        &envelope.user_id,
                        envelope.payload,
                    )
                    .await
                }
                _ => unreachable!("kind validated above"),
            };
            if let Err(error) = result {
                crate::observability::metrics::MetricsManager::record_redis_realtime(
                    match envelope.kind.as_str() {
                        "storage" => "storage",
                        _ => "mail_runtime",
                    },
                    "dispatch_failed",
                );
                Logger::sys_error(
                    "redis.realtime",
                    "Centrifugo realtime dispatch failed",
                    &error.to_string(),
                );
            } else {
                crate::observability::metrics::MetricsManager::record_redis_realtime(
                    match envelope.kind.as_str() {
                        "storage" => "storage",
                        _ => "mail_runtime",
                    },
                    "delivered",
                );
            }
        }
        Ok(())
    }
}
