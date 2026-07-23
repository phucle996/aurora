use futures_util::StreamExt;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::{oneshot, Mutex};
use uuid::Uuid;

// [COMMENT]: Một PubSub reply socket dùng chung cho toàn pod; connect-proxy hot path
// chỉ publish qua multiplexed Redis connection và chờ oneshot theo request_id.
pub struct SharedRedisRequestBus {
    client: Arc<redis::Client>,
    pending: Arc<Mutex<HashMap<String, oneshot::Sender<Vec<u8>>>>>,
}

impl SharedRedisRequestBus {
    pub async fn new(url: &str) -> Result<Arc<Self>, String> {
        let client = Arc::new(
            redis::Client::open(url)
                .map_err(|error| format!("open Shared Redis client: {error}"))?,
        );
        let connection = client
            .get_async_connection()
            .await
            .map_err(|error| format!("connect Shared Redis PubSub: {error}"))?;
        let mut subscriber = connection.into_pubsub();
        subscriber
            .psubscribe("*.reply.*")
            .await
            .map_err(|error| format!("subscribe Shared Redis replies: {error}"))?;

        let bus = Arc::new(Self {
            client,
            pending: Arc::new(Mutex::new(HashMap::new())),
        });
        let client = bus.client.clone();
        let pending = bus.pending.clone();
        tokio::spawn(async move {
            let mut active = Some(subscriber);
            loop {
                if let Some(mut subscriber) = active.take() {
                    let mut messages = subscriber.on_message();
                    while let Some(message) = messages.next().await {
                        let channel = message.get_channel_name().to_string();
                        if let Some(sender) = pending.lock().await.remove(&channel) {
                            let _ = sender.send(message.get_payload_bytes().to_vec());
                        }
                    }
                }
                // [COMMENT]: Request trong reconnect window timeout fail-close; pod tự
                // khôi phục reply subscription sau Redis failover mà không cần restart.
                tokio::time::sleep(Duration::from_millis(500)).await;
                if let Ok(connection) = client.get_async_connection().await {
                    let mut subscriber = connection.into_pubsub();
                    if subscriber.psubscribe("*.reply.*").await.is_ok() {
                        active = Some(subscriber);
                    }
                }
            }
        });
        Ok(bus)
    }

    pub async fn request(
        &self,
        request_channel: &str,
        reply_prefix: &str,
        protobuf: Vec<u8>,
    ) -> Result<Vec<u8>, String> {
        let request_id = Uuid::new_v4();
        let reply_channel = format!("{reply_prefix}{request_id}");
        let (sender, receiver) = oneshot::channel();
        self.pending
            .lock()
            .await
            .insert(reply_channel.clone(), sender);

        let mut envelope = Vec::with_capacity(16 + protobuf.len());
        envelope.extend_from_slice(request_id.as_bytes());
        envelope.extend_from_slice(&protobuf);

        let mut connection = self
            .client
            .get_multiplexed_tokio_connection()
            .await
            .map_err(|error| format!("connect Shared Redis publisher: {error}"))?;
        let subscribers: i64 = connection
            .publish(request_channel, envelope)
            .await
            .map_err(|error| format!("publish Shared Redis auth request: {error}"))?;
        if subscribers == 0 {
            self.pending.lock().await.remove(&reply_channel);
            return Err("no ACR replica subscribed to auth request".to_string());
        }

        match tokio::time::timeout(Duration::from_secs(5), receiver).await {
            Ok(Ok(payload)) => Ok(payload),
            Ok(Err(_)) => {
                self.pending.lock().await.remove(&reply_channel);
                Err("Shared Redis reply router stopped".to_string())
            }
            Err(_) => {
                self.pending.lock().await.remove(&reply_channel);
                Err("Shared Redis auth request timed out".to_string())
            }
        }
    }
}
