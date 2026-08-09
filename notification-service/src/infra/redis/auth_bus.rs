use crate::application::auth::AuthCredentials;
use crate::application::ports::{AppError, AuthError, AuthVerifier, AuthenticatedPrincipal};
use crate::config::RedisConfig;
use crate::contract::trinity::rpc::{
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse, VerifyUserTrinityTokenRequest,
    VerifyUserTrinityTokenResponse,
};
use crate::infra::vault::VaultClient;
use crate::observability::{logger::Logger, metrics::MetricsManager};
use futures_util::future::BoxFuture;
use futures_util::StreamExt;
use prost::Message;
use redis::aio::ConnectionManager;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;
use tokio::sync::{oneshot, Mutex};
use tokio::task::JoinHandle;
use uuid::Uuid;

const MAX_REPLY_BYTES: usize = 64 * 1024;
const CONNECTION_PATH: &str = "secret/data/connections/redis/shared-l2/role-notification-consume";

#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    url: String,
}

/// Shared Redis request/reply adapter. The reply socket is owned by one
/// supervised task per pod; request waiters are bounded to prevent a Redis
/// outage from turning into unbounded heap growth.
pub struct RedisAuthBus {
    client: Arc<redis::Client>,
    publisher: ConnectionManager,
    pending: Arc<Mutex<HashMap<String, oneshot::Sender<Vec<u8>>>>>,
    connect_timeout: Duration,
    timeout: Duration,
    max_pending: usize,
    reconnect_initial: Duration,
    reconnect_max: Duration,
}

impl RedisAuthBus {
    pub async fn connect(config: &RedisConfig, vault: &VaultClient) -> Result<Arc<Self>, AppError> {
        let record: ConnectionRecord = vault
            .read(CONNECTION_PATH)
            .await
            .map_err(|error| Box::new(std::io::Error::other(error)) as AppError)?;
        if record.schema_version != 1
            || (!record.url.starts_with("redis://") && !record.url.starts_with("rediss://"))
        {
            return Err(Box::new(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "invalid Vault Shared Redis connection record",
            )));
        }
        let client = Arc::new(redis::Client::open(record.url)?);
        let publisher =
            tokio::time::timeout(config.connect_timeout, client.get_connection_manager()).await??;

        // Fail fast on a missing Redis ACL/network route. The runtime still
        // reconnects the reply socket after a later failover.
        let mut subscriber =
            tokio::time::timeout(config.connect_timeout, client.get_async_pubsub()).await??;
        subscriber.psubscribe("*.reply.*").await?;
        drop(subscriber);

        Ok(Arc::new(Self {
            client,
            publisher,
            pending: Arc::new(Mutex::new(HashMap::new())),
            connect_timeout: config.connect_timeout,
            timeout: config.auth_timeout,
            max_pending: config.auth_max_pending,
            reconnect_initial: config.reconnect_initial,
            reconnect_max: config.reconnect_max,
        }))
    }

    pub fn client(&self) -> Arc<redis::Client> {
        self.client.clone()
    }

    pub fn spawn_reply_router(self: &Arc<Self>, shutdown: watch::Receiver<bool>) -> JoinHandle<()> {
        let client = self.client.clone();
        let pending = self.pending.clone();
        let connect_timeout = self.connect_timeout;
        let reconnect_initial = self.reconnect_initial;
        let reconnect_max = self.reconnect_max;
        tokio::spawn(async move {
            run_reply_router(
                client,
                pending,
                connect_timeout,
                reconnect_initial,
                reconnect_max,
                shutdown,
            )
            .await;
        })
    }

    async fn request(
        &self,
        request_channel: &'static str,
        reply_prefix: &'static str,
        protobuf: Vec<u8>,
    ) -> Result<Vec<u8>, AuthError> {
        let request_id = Uuid::new_v4();
        let reply_channel = format!("{reply_prefix}{request_id}");
        let (sender, receiver) = oneshot::channel();
        {
            let mut pending = self.pending.lock().await;
            if pending.len() >= self.max_pending {
                return Err(AuthError::Unavailable(
                    "authentication request capacity exhausted".to_owned(),
                ));
            }
            pending.insert(reply_channel.clone(), sender);
        }

        let mut envelope = Vec::with_capacity(16 + protobuf.len());
        envelope.extend_from_slice(request_id.as_bytes());
        envelope.extend_from_slice(&protobuf);

        let mut connection = self.publisher.clone();
        let subscribers: i64 =
            match tokio::time::timeout(self.timeout, connection.publish(request_channel, envelope))
                .await
            {
                Ok(Ok(subscribers)) => subscribers,
                Ok(Err(error)) => {
                    self.pending.lock().await.remove(&reply_channel);
                    return Err(AuthError::Unavailable(format!(
                        "Shared Redis request publish failed: {error}"
                    )));
                }
                Err(_) => {
                    self.pending.lock().await.remove(&reply_channel);
                    return Err(AuthError::Unavailable(
                        "Shared Redis auth request publish timed out".to_owned(),
                    ));
                }
            };
        if subscribers == 0 {
            self.pending.lock().await.remove(&reply_channel);
            return Err(AuthError::Unavailable(
                "no ACR replica subscribed to auth request".to_owned(),
            ));
        }

        match tokio::time::timeout(self.timeout, receiver).await {
            Ok(Ok(payload)) => Ok(payload),
            Ok(Err(_)) => {
                self.pending.lock().await.remove(&reply_channel);
                Err(AuthError::Unavailable(
                    "Shared Redis reply router stopped".to_owned(),
                ))
            }
            Err(_) => {
                self.pending.lock().await.remove(&reply_channel);
                Err(AuthError::Unavailable(
                    "Shared Redis auth request timed out".to_owned(),
                ))
            }
        }
    }

    async fn verify_credentials(
        &self,
        credentials: AuthCredentials,
    ) -> Result<AuthenticatedPrincipal, AuthError> {
        let started = std::time::Instant::now();
        let (channel, reply_prefix, payload, kind) = match credentials {
            AuthCredentials::Admin {
                access_token,
                access_key,
                access_secret,
            } => {
                let request = VerifyAdminTrinityTokenRequest {
                    access_token,
                    access_key,
                    access_secret,
                };
                let mut payload = Vec::new();
                request.encode(&mut payload).map_err(|error| {
                    AuthError::Protocol(format!("encode admin verification request: {error}"))
                })?;
                (
                    "iam.auth.verify_admin_trinity",
                    "iam.auth.verify_admin_trinity.reply.",
                    payload,
                    "verify_admin_trinity_token",
                )
            }
            AuthCredentials::User {
                access_token,
                access_key,
                access_secret,
            } => {
                let request = VerifyUserTrinityTokenRequest {
                    access_token,
                    access_key,
                    access_secret,
                };
                let mut payload = Vec::new();
                request.encode(&mut payload).map_err(|error| {
                    AuthError::Protocol(format!("encode user verification request: {error}"))
                })?;
                (
                    "iam.auth.verify_user_trinity",
                    "iam.auth.verify_user_trinity.reply.",
                    payload,
                    "verify_user_trinity_token",
                )
            }
        };

        let result = match self.request(channel, reply_prefix, payload).await {
            Ok(response) if kind == "verify_admin_trinity_token" => {
                let response = VerifyAdminTrinityTokenResponse::decode(response.as_slice())
                    .map_err(|error| {
                        AuthError::Protocol(format!("decode admin response: {error}"))
                    })?;
                if response.valid {
                    valid_principal(response.admin_id)
                } else {
                    Err(AuthError::Invalid)
                }
            }
            Ok(response) => {
                let response = VerifyUserTrinityTokenResponse::decode(response.as_slice())
                    .map_err(|error| {
                        AuthError::Protocol(format!("decode user response: {error}"))
                    })?;
                if response.valid {
                    valid_principal(response.user_id)
                } else {
                    Err(AuthError::Invalid)
                }
            }
            Err(error) => Err(error),
        };

        let status = match &result {
            Ok(_) => "ok",
            Err(AuthError::Invalid) => "invalid",
            Err(AuthError::Unavailable(_)) => "timeout",
            Err(AuthError::Protocol(_)) => "error",
        };
        MetricsManager::record_redis_call(kind, status, started.elapsed());
        result
    }
}

impl AuthVerifier for RedisAuthBus {
    fn verify<'a>(
        &'a self,
        credentials: AuthCredentials,
    ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>> {
        Box::pin(self.verify_credentials(credentials))
    }
}

fn valid_principal(id: String) -> Result<AuthenticatedPrincipal, AuthError> {
    if uuid::Uuid::parse_str(&id).is_err() {
        return Err(AuthError::Protocol(
            "ACR returned a non-UUID principal".to_owned(),
        ));
    }
    Ok(AuthenticatedPrincipal { id })
}

async fn run_reply_router(
    client: Arc<redis::Client>,
    pending: Arc<Mutex<HashMap<String, oneshot::Sender<Vec<u8>>>>>,
    connect_timeout: Duration,
    reconnect_initial: Duration,
    reconnect_max: Duration,
    mut shutdown: watch::Receiver<bool>,
) {
    let mut delay = reconnect_initial;
    loop {
        if *shutdown.borrow() {
            return;
        }

        match tokio::time::timeout(connect_timeout, client.get_async_pubsub()).await {
            Ok(Ok(mut subscriber)) => {
                if subscriber.psubscribe("*.reply.*").await.is_ok() {
                    Logger::sys_info(
                        "redis.auth_reply",
                        "Shared Redis auth reply router connected",
                    );
                    delay = reconnect_initial;
                    let mut messages = subscriber.on_message();
                    loop {
                        tokio::select! {
                            changed = shutdown.changed() => {
                                if changed.is_ok() && *shutdown.borrow() {
                                    return;
                                }
                            }
                            message = messages.next() => {
                                match message {
                                    Some(message) => {
                                        let channel = message.get_channel_name().to_owned();
                                        let payload = message.get_payload_bytes();
                                        if payload.len() > MAX_REPLY_BYTES {
                                            continue;
                                        }
                                        if let Some(sender) = pending.lock().await.remove(&channel) {
                                            let _ = sender.send(payload.to_vec());
                                        }
                                    }
                                    None => break,
                                }
                            }
                        }
                    }
                }
            }
            Ok(Err(error)) => {
                Logger::sys_error(
                    "redis.auth_reply",
                    "Shared Redis auth reply router disconnected",
                    &error.to_string(),
                );
            }
            Err(error) => {
                Logger::sys_error(
                    "redis.auth_reply",
                    "Shared Redis auth reply connection timed out",
                    &error.to_string(),
                );
            }
        }

        tokio::select! {
            changed = shutdown.changed() => {
                if changed.is_ok() && *shutdown.borrow() {
                    return;
                }
            }
            _ = tokio::time::sleep(delay) => {
                delay = std::cmp::min(delay.saturating_mul(2), reconnect_max);
            }
        }
    }
}
