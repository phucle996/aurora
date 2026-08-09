use crate::application::runtime_updates::{DispatchOutcome, RuntimeUpdateService};
use crate::config::RuntimeConfig;
use crate::contract::realtime::{RealtimeEnvelope, REALTIME_CHANNEL};
use crate::observability::{logger::Logger, metrics::MetricsManager};
use futures_util::StreamExt;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;

pub struct RealtimePubSubConsumer {
    client: Arc<redis::Client>,
    dispatcher: Arc<RuntimeUpdateService>,
    config: RuntimeConfig,
    connect_timeout: Duration,
    shutdown: watch::Receiver<bool>,
}

impl RealtimePubSubConsumer {
    pub fn new(
        client: Arc<redis::Client>,
        dispatcher: Arc<RuntimeUpdateService>,
        config: RuntimeConfig,
        connect_timeout: Duration,
        shutdown: watch::Receiver<bool>,
    ) -> Self {
        Self {
            client,
            dispatcher,
            config,
            connect_timeout,
            shutdown,
        }
    }

    pub async fn run(mut self) {
        let mut delay = self.config.reconnect_initial;
        loop {
            if *self.shutdown.borrow() {
                return;
            }
            match self.listen_once().await {
                Ok(()) => delay = self.config.reconnect_initial,
                Err(error) => {
                    Logger::sys_error(
                        "redis.realtime_pubsub",
                        "Shared Redis realtime Pub/Sub disconnected; reconnecting",
                        &error.to_string(),
                    );
                    if wait_for_shutdown(&mut self.shutdown, delay).await {
                        return;
                    }
                    delay = std::cmp::min(delay.saturating_mul(2), self.config.reconnect_max);
                }
            }
        }
    }

    async fn listen_once(&mut self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let mut subscriber =
            tokio::time::timeout(self.connect_timeout, self.client.get_async_pubsub()).await??;
        subscriber.subscribe(REALTIME_CHANNEL).await?;
        Logger::sys_info(
            "redis.realtime_pubsub",
            "Listening for Central realtime notifications on Shared Redis",
        );

        let mut messages = subscriber.on_message();
        loop {
            tokio::select! {
                changed = self.shutdown.changed() => {
                    if changed.is_ok() && *self.shutdown.borrow() {
                        return Ok(());
                    }
                }
                message = messages.next() => {
                    let Some(message) = message else {
                        return Err("Shared Redis Pub/Sub stream ended".into());
                    };
                    let raw = message.get_payload_bytes();
                    let envelope = match RealtimeEnvelope::parse(raw) {
                        Ok(envelope) => envelope,
                        Err(reason) => {
                            MetricsManager::record_redis_realtime("unknown", normalize_rejection(reason));
                            Logger::sys_warn(
                                "redis.realtime_pubsub",
                                "Dropped invalid realtime notification envelope",
                                reason,
                            );
                            continue;
                        }
                    };
                    let kind = match envelope.kind.as_str() {
                        "storage" => "storage",
                        "mail_runtime" => "mail_runtime",
                        _ => "other",
                    };
                    match self.dispatcher.dispatch(envelope).await {
                        Ok(DispatchOutcome::Published) => {
                            MetricsManager::record_redis_realtime(kind, "success");
                        }
                        Ok(DispatchOutcome::Dropped) => {
                            MetricsManager::record_redis_realtime(kind, "invalid");
                        }
                        Err(error) => {
                            MetricsManager::record_redis_realtime(kind, "delivery_failed");
                            Logger::sys_error(
                                "redis.realtime_pubsub",
                                "Centrifugo realtime dispatch failed",
                                &error.to_string(),
                            );
                        }
                    }
                }
            }
        }
    }
}

fn normalize_rejection(reason: &str) -> &'static str {
    if reason == "REALTIME_ENVELOPE_TOO_LARGE" {
        "oversized"
    } else {
        "invalid"
    }
}

async fn wait_for_shutdown(shutdown: &mut watch::Receiver<bool>, delay: Duration) -> bool {
    tokio::select! {
        changed = shutdown.changed() => changed.is_ok() && *shutdown.borrow(),
        _ = tokio::time::sleep(delay) => false,
    }
}
