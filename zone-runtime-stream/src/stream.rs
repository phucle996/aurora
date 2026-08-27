use std::{collections::HashMap, sync::Arc, time::Duration};

use tokio::sync::{broadcast, Mutex, Semaphore};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    config::Config,
    contract::{RuntimeFrame, RuntimeScope, SubscriptionKey},
    source::{SourceError, VictoriaSource},
    telemetry::Telemetry,
};

pub(crate) struct Subscription {
    pub(crate) sender: broadcast::Sender<RuntimeFrame>,
    pub(crate) clients: std::sync::atomic::AtomicUsize,
    pub(crate) stop: CancellationToken,
}

impl Subscription {
    fn new(buffer: usize) -> Arc<Self> {
        let (sender, _) = broadcast::channel(buffer);
        Arc::new(Self {
            sender,
            clients: std::sync::atomic::AtomicUsize::new(0),
            stop: CancellationToken::new(),
        })
    }
}

#[derive(Clone)]
pub struct RuntimeStream {
    config: Config,
    source: Arc<VictoriaSource>,
    shutdown: CancellationToken,
    connections: Arc<Semaphore>,
    subscriptions: Arc<Mutex<HashMap<SubscriptionKey, Arc<Subscription>>>>,
    telemetry: Arc<Telemetry>,
}

impl RuntimeStream {
    pub fn new(config: Config, source: VictoriaSource, shutdown: CancellationToken) -> Self {
        Self {
            connections: Arc::new(Semaphore::new(config.max_connections)),
            config,
            source: Arc::new(source),
            shutdown,
            subscriptions: Arc::new(Mutex::new(HashMap::new())),
            telemetry: Arc::new(Telemetry::default()),
        }
    }

    pub async fn subscribe(
        &self,
        scope: RuntimeScope,
    ) -> Result<
        (
            broadcast::Receiver<RuntimeFrame>,
            tokio::sync::OwnedSemaphorePermit,
            Arc<Subscription>,
        ),
        SubscribeError,
    > {
        let permit = match self.connections.clone().try_acquire_owned() {
            Ok(permit) => permit,
            Err(_) => {
                self.telemetry.connection_rejected();
                tracing::warn!(
                    event_code = "ZONE_RUNTIME_STREAM_CONNECTION_REJECTED",
                    outcome = "capacity"
                );
                return Err(SubscribeError::Capacity);
            }
        };
        let key = SubscriptionKey {
            scope: scope.clone(),
        };
        let mut subscriptions = self.subscriptions.lock().await;
        let (subscription, created) = if let Some(existing) = subscriptions.get(&key) {
            (existing.clone(), false)
        } else {
            if subscriptions.len() >= self.config.max_fanout_groups {
                self.telemetry.connection_rejected();
                tracing::warn!(
                    event_code = "ZONE_RUNTIME_STREAM_CONNECTION_REJECTED",
                    outcome = "fanout_capacity"
                );
                return Err(SubscribeError::Capacity);
            }
            let subscription = Subscription::new(self.config.max_buffered_events);
            subscriptions.insert(key.clone(), subscription.clone());
            self.telemetry.fanout_group_opened();
            (subscription, true)
        };
        subscription
            .clients
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        self.telemetry.connection_opened();
        let receiver = subscription.sender.subscribe();
        drop(subscriptions);
        if created {
            // Attach the first receiver before the reader's immediate interval
            // tick so the required initial snapshot cannot be sent to zero
            // subscribers and silently lost.
            self.spawn_reader(key, subscription.clone());
        }
        Ok((receiver, permit, subscription))
    }

    fn spawn_reader(&self, key: SubscriptionKey, subscription: Arc<Subscription>) {
        let source = self.source.clone();
        let shutdown = self.shutdown.clone();
        let interval = self.config.query_interval;
        let telemetry = self.telemetry.clone();
        tokio::spawn(async move {
            let mut sequence = 0_u64;
            let mut ticker = tokio::time::interval(interval);
            loop {
                tokio::select! {
                    _ = shutdown.cancelled() => break,
                    _ = subscription.stop.cancelled() => break,
                    _ = ticker.tick() => {
                        sequence = sequence.saturating_add(1);
                        // Resource identities must not be copied into browser-visible SSE IDs.
                        // The cursor is only meaningful inside this ephemeral subscription.
                        let event_id = format!("r{sequence}");
                        telemetry.source_query();
                        match source
                            .read(&key.scope, event_id.clone(), sequence == 1)
                            .await
                        {
                            Ok(event) => {
                                let _ = subscription.sender.send(RuntimeFrame::Event(event));
                            }
                            Err(error) => {
                                telemetry.source_error();
                                let _ = subscription.sender.send(RuntimeFrame::Error {
                                    event_id,
                                    code: source_error_code(&error).to_string(),
                                });
                            }
                        }
                    }
                }
            }
            subscription.stop.cancel();
        });
    }

    pub async fn remove_if_unused(&self, scope: &RuntimeScope, subscription: &Arc<Subscription>) {
        if subscription
            .clients
            .load(std::sync::atomic::Ordering::Relaxed)
            != 0
        {
            return;
        }
        let key = SubscriptionKey {
            scope: scope.clone(),
        };
        let mut subscriptions = self.subscriptions.lock().await;
        if subscriptions
            .get(&key)
            .is_some_and(|current| Arc::ptr_eq(current, subscription))
            && subscription
                .clients
                .load(std::sync::atomic::Ordering::Acquire)
                == 0
        {
            subscriptions.remove(&key);
            subscription.stop.cancel();
            self.telemetry.fanout_group_closed();
        }
    }

    pub async fn shutdown(&self) {
        self.shutdown.cancel();
        let subscriptions = self.subscriptions.lock().await;
        for subscription in subscriptions.values() {
            subscription.stop.cancel();
        }
    }

    pub fn max_lifetime(&self) -> Duration {
        self.config.max_lifetime
    }

    pub fn heartbeat(&self) -> Duration {
        self.config.heartbeat
    }

    pub fn max_snapshot(&self) -> Duration {
        self.config.max_snapshot
    }

    pub fn zone_id(&self) -> Uuid {
        self.config.zone_id
    }

    pub fn connection_closed(&self) {
        self.telemetry.connection_closed();
    }

    pub fn gap_event(&self) {
        self.telemetry.gap_event();
    }

    pub fn stream_expired(&self) {
        self.telemetry.stream_expired();
    }

    pub fn prometheus_metrics(&self) -> String {
        self.telemetry.prometheus()
    }
}

#[derive(Debug, thiserror::Error)]
pub enum SubscribeError {
    #[error("runtime stream capacity is exhausted")]
    Capacity,
}

fn source_error_code(error: &SourceError) -> &'static str {
    match error {
        SourceError::Request(_) => "VICTORIA_UNAVAILABLE",
        SourceError::Status => "VICTORIA_UNAVAILABLE",
        SourceError::ResponseTooLarge => "VICTORIA_RESPONSE_TOO_LARGE",
        SourceError::Decode => "VICTORIA_RESPONSE_INVALID",
        SourceError::Scope => "RUNTIME_SCOPE_INVALID",
    }
}

pub fn next_event_id() -> String {
    Uuid::new_v4().to_string()
}

#[cfg(test)]
#[path = "../test/stream.rs"]
mod tests;
