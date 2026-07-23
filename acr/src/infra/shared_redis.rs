use crate::observability::logger::Logger;
use futures_util::StreamExt;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::{oneshot, Mutex};
use uuid::Uuid;

// [COMMENT]: SharedRedisBus giữ một PubSub connection lâu dài cho reply fan-in.
// Mỗi request chỉ cần một multiplexed publish, tránh mở hai TCP connection trên hot path login.
pub struct SharedRedisBus {
    client: Arc<redis::Client>,
    pending: Arc<Mutex<HashMap<String, oneshot::Sender<Vec<u8>>>>>,
}

impl SharedRedisBus {
    pub async fn new(client: Arc<redis::Client>) -> Result<Arc<Self>, String> {
        let connection = client
            .get_async_connection()
            .await
            .map_err(|error| format!("open Shared Redis PubSub connection: {error}"))?;
        let mut pubsub = connection.into_pubsub();
        pubsub
            .psubscribe("*.reply.*")
            .await
            .map_err(|error| format!("subscribe Shared Redis reply channels: {error}"))?;

        let bus = Arc::new(Self {
            client,
            pending: Arc::new(Mutex::new(HashMap::new())),
        });

        let client = bus.client.clone();
        let pending = bus.pending.clone();
        tokio::spawn(async move {
            // [COMMENT]: Khi Redis failover làm rớt PubSub socket, request đang bay sẽ timeout
            // fail-close; router tự nối lại để pod không cần restart.
            let mut active = Some(pubsub);
            loop {
                if let Some(mut subscriber) = active.take() {
                    let mut messages = subscriber.on_message();
                    while let Some(message) = messages.next().await {
                        let channel = message.get_channel_name().to_string();
                        let payload = message.get_payload_bytes().to_vec();
                        if let Some(sender) = pending.lock().await.remove(&channel) {
                            let _ = sender.send(payload);
                        }
                    }
                }

                Logger::sys_warn(
                    "shared_redis.reply_router",
                    "Shared Redis reply subscription disconnected; reconnecting",
                    "redis_pubsub_disconnected",
                );
                tokio::time::sleep(Duration::from_millis(500)).await;

                match client.get_async_connection().await {
                    Ok(connection) => {
                        let mut subscriber = connection.into_pubsub();
                        match subscriber.psubscribe("*.reply.*").await {
                            Ok(()) => active = Some(subscriber),
                            Err(error) => Logger::sys_error(
                                "shared_redis.reply_router",
                                "Failed to restore reply subscription",
                                &error.to_string(),
                            ),
                        }
                    }
                    Err(error) => Logger::sys_error(
                        "shared_redis.reply_router",
                        "Failed to reconnect Shared Redis",
                        &error.to_string(),
                    ),
                }
            }
        });

        Ok(bus)
    }

    pub async fn append_stream(
        &self,
        stream: &str,
        protobuf_payload: Vec<u8>,
    ) -> Result<String, String> {
        let mut connection = self
            .client
            .get_multiplexed_tokio_connection()
            .await
            .map_err(|error| format!("open Shared Redis Stream connection: {error}"))?;
        redis::cmd("XADD")
            .arg(stream)
            .arg("*")
            .arg("payload")
            .arg(protobuf_payload)
            .query_async(&mut connection)
            .await
            .map_err(|error| format!("append Shared Redis Stream event: {error}"))
    }

    pub async fn request(
        &self,
        request_channel: &str,
        reply_prefix: &str,
        protobuf_payload: Vec<u8>,
        timeout: Duration,
    ) -> Result<Vec<u8>, String> {
        let request_id = Uuid::now_v7();
        let reply_channel = format!("{reply_prefix}{request_id}");
        let (sender, receiver) = oneshot::channel();

        // [COMMENT]: Đăng ký waiter trước khi publish để response nhanh không thể vượt qua
        // consumer và biến thành lost wake-up.
        self.pending
            .lock()
            .await
            .insert(reply_channel.clone(), sender);

        let mut envelope = Vec::with_capacity(16 + protobuf_payload.len());
        envelope.extend_from_slice(request_id.as_bytes());
        envelope.extend_from_slice(&protobuf_payload);

        let publish_result = async {
            let mut connection = self
                .client
                .get_multiplexed_tokio_connection()
                .await
                .map_err(|error| format!("open Shared Redis publish connection: {error}"))?;
            let subscribers: i64 = connection
                .publish(request_channel, envelope)
                .await
                .map_err(|error| format!("publish Shared Redis request: {error}"))?;
            if subscribers == 0 {
                return Err("Shared Redis request has no active consumer".to_string());
            }
            Ok(())
        }
        .await;

        if let Err(error) = publish_result {
            self.pending.lock().await.remove(&reply_channel);
            return Err(error);
        }

        match tokio::time::timeout(timeout, receiver).await {
            Ok(Ok(payload)) => Ok(payload),
            Ok(Err(_)) => {
                self.pending.lock().await.remove(&reply_channel);
                Err("Shared Redis reply router stopped before response".to_string())
            }
            Err(_) => {
                self.pending.lock().await.remove(&reply_channel);
                Err("Shared Redis request timed out".to_string())
            }
        }
    }

    pub async fn publish_event(
        &self,
        channel: &str,
        protobuf_payload: Vec<u8>,
    ) -> Result<i64, String> {
        let event_id = Uuid::now_v7();
        let mut envelope = Vec::with_capacity(16 + protobuf_payload.len());
        envelope.extend_from_slice(event_id.as_bytes());
        envelope.extend_from_slice(&protobuf_payload);

        let mut connection = self
            .client
            .get_multiplexed_tokio_connection()
            .await
            .map_err(|error| format!("open Shared Redis publish connection: {error}"))?;
        connection
            .publish(channel, envelope)
            .await
            .map_err(|error| format!("publish Shared Redis event: {error}"))
    }
}
