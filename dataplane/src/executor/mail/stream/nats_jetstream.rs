use super::{RuntimeGenerationFence, StreamRuntimeContext};
use crate::executor::mail::runtime_configuration::RuntimeConsumerConfiguration;
use crate::executor::mail::runtime_proto::{MailStreamType, NatsJetStreamPayloadV1};
use crate::executor::mail::stream_processor::MailProcessingStatus;
use crate::infra::zone_kv::ZoneLease;
use async_nats::jetstream::{self, consumer::PullConsumer, AckKind};
use futures_util::StreamExt;
use prost::Message;
use serde::Deserialize;
use std::sync::Arc;
use std::time::Duration;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use zeroize::Zeroize;

const SUBMISSION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0x1d, 0x35, 0x9f, 0x0e, 0x7a, 0x36, 0x50, 0x5c, 0xa6, 0x59, 0x1c, 0x70, 0x47, 0xf7, 0x16, 0xaa,
]);
const MAX_SAFE_RETRIES: u8 = 5;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct NatsJetStreamConnectionConfigV1 {
    servers: String,
    auth_type: String,
    #[serde(default)]
    username: Option<String>,
    #[serde(default)]
    password: Option<String>,
    #[serde(default)]
    token: Option<String>,
}

impl Drop for NatsJetStreamConnectionConfigV1 {
    fn drop(&mut self) {
        self.servers.zeroize();
        self.auth_type.zeroize();
        self.username.zeroize();
        self.password.zeroize();
        self.token.zeroize();
    }
}

/// [COMMENT]: JetStream suite dùng durable pull consumer và Ack/Nak/Term native; không dùng Zone KV làm message ledger.
pub async fn run(
    context: Arc<StreamRuntimeContext>,
    configuration: Arc<RuntimeConsumerConfiguration>,
    slot: u32,
    generation: u64,
    lease: ZoneLease,
    generation_fence: Arc<RuntimeGenerationFence>,
    cancel: CancellationToken,
) {
    let payload = match NatsJetStreamPayloadV1::decode(configuration.stream.payload.as_slice()) {
        Ok(payload) if configuration.stream.payload_schema_version == 1 => payload,
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_PAYLOAD_INVALID",
                )
                .await;
            return;
        }
    };
    let plaintext = match context.decrypt_connection(
        MailStreamType::NatsJetstream,
        configuration.stream.broker_resource_id,
        &payload.source_config_envelope,
    ) {
        Ok(plaintext) => plaintext,
        Err(code) => {
            context
                .write_health("ERROR", &configuration, slot, generation, &lease, code)
                .await;
            return;
        }
    };
    let connection = match serde_json::from_slice::<NatsJetStreamConnectionConfigV1>(&plaintext) {
        Ok(connection)
            if !connection.servers.trim().is_empty()
                && connection.servers.len() <= 4_096
                && connection.servers.split(',').count() <= 32
                && connection
                    .servers
                    .split(',')
                    .all(|server| server.trim().starts_with("tls://")) =>
        {
            connection
        }
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONNECTION_CONFIG_INVALID",
                )
                .await;
            return;
        }
    };
    drop(plaintext);
    let server_addresses = match connection
        .servers
        .split(',')
        .map(str::trim)
        .map(str::parse::<async_nats::ServerAddr>)
        .collect::<Result<Vec<_>, _>>()
    {
        Ok(addresses) if !addresses.is_empty() => addresses,
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONNECTION_CONFIG_INVALID",
                )
                .await;
            return;
        }
    };
    let mut options = async_nats::ConnectOptions::new().require_tls(true);
    options = match connection.auth_type.as_str() {
        "token" => match connection.token.as_ref() {
            Some(token) if !token.is_empty() => options.token(token.clone()),
            _ => {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_NATS_JETSTREAM_CREDENTIAL_REQUIRED",
                    )
                    .await;
                return;
            }
        },
        "user_password" => match (connection.username.as_ref(), connection.password.as_ref()) {
            (Some(username), Some(password)) if !username.is_empty() && !password.is_empty() => {
                options.user_and_password(username.clone(), password.clone())
            }
            _ => {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_NATS_JETSTREAM_CREDENTIAL_REQUIRED",
                    )
                    .await;
                return;
            }
        },
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_AUTH_UNSUPPORTED",
                )
                .await;
            return;
        }
    };
    let client = match tokio::time::timeout(
        Duration::from_secs(10),
        async_nats::connect_with_options(server_addresses, options),
    )
    .await
    {
        Ok(Ok(client)) => client,
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONNECTION_FAILED",
                )
                .await;
            return;
        }
        Ok(Err(_)) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONNECTION_FAILED",
                )
                .await;
            return;
        }
    };
    drop(connection);
    let jetstream = jetstream::new(client);
    let stream = match tokio::time::timeout(
        Duration::from_secs(10),
        jetstream.get_stream(&payload.stream_name),
    )
    .await
    {
        Ok(Ok(stream)) => stream,
        Ok(Err(_)) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_STREAM_NOT_FOUND",
                )
                .await;
            return;
        }
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_STREAM_NOT_FOUND",
                )
                .await;
            return;
        }
    };
    let consumer: PullConsumer = match tokio::time::timeout(
        Duration::from_secs(10),
        stream.get_consumer(&payload.durable_name),
    )
    .await
    {
        Ok(Ok(consumer)) => consumer,
        Ok(Err(_)) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONSUMER_NOT_FOUND",
                )
                .await;
            return;
        }
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_NATS_JETSTREAM_CONSUMER_NOT_FOUND",
                )
                .await;
            return;
        }
    };
    let mut messages =
        match tokio::time::timeout(Duration::from_secs(10), consumer.messages()).await {
            Ok(Ok(messages)) => messages,
            Ok(Err(_)) => {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_NATS_JETSTREAM_PULL_FAILED",
                    )
                    .await;
                return;
            }
            Err(_) => {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_NATS_JETSTREAM_PULL_FAILED",
                    )
                    .await;
                return;
            }
        };

    context
        .write_health("RUNNING", &configuration, slot, generation, &lease, "")
        .await;
    let renew_every = (context.lease_ttl / 3).max(Duration::from_secs(1));
    let mut renew = tokio::time::interval(renew_every);
    renew.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut tasks = JoinSet::<Result<(), &'static str>>::new();

    loop {
        tokio::select! {
            _ = cancel.cancelled() => break,
            _ = renew.tick() => {
                if !context.renew_and_report(&configuration, slot, generation, &lease, &generation_fence).await {
                    break;
                }
            }
            completed = tasks.join_next(), if !tasks.is_empty() => {
                match completed {
                    Some(Ok(Ok(()))) | None => {}
                    Some(Ok(Err(code))) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, code).await;
                        break;
                    }
                    Some(Err(_)) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_NATS_JETSTREAM_PROCESSING_TASK_FAILED").await;
                        break;
                    }
                }
            }
            next = messages.next(), if tasks.len() < context.max_slot_inflight => {
                let Some(next) = next else {
                    context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_NATS_JETSTREAM_PULL_CLOSED").await;
                    break;
                };
                let message = match next {
                    Ok(message) => message,
                    Err(_) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_NATS_JETSTREAM_PULL_FAILED").await;
                        break;
                    }
                };
                if message.payload.len() > context.max_message_bytes {
                    // [COMMENT]: Oversized customer payload là permanent transport violation; không đưa vào processor/JMAP queue.
                    if message.ack_with(AckKind::Term).await.is_err() {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_NATS_JETSTREAM_ACK_FAILED").await;
                        break;
                    }
                    continue;
                }
                let stream_sequence = match message.info() {
                    Ok(info) => info.stream_sequence,
                    Err(_) => {
                        if message.ack_with(AckKind::Term).await.is_err() {
                            context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_NATS_JETSTREAM_ACK_FAILED").await;
                            break;
                        }
                        continue;
                    }
                };
                let processor = context.processor.clone();
                let work_configuration = configuration.clone();
                let work_fence = generation_fence.clone();
                let stream_name = payload.stream_name.clone();
                tasks.spawn(async move {
                    let submission_id = uuid::Uuid::new_v5(
                        &SUBMISSION_NAMESPACE,
                        format!("{}\0nats-jetstream\0{}\0{}", work_configuration.consumer_id, stream_name, stream_sequence).as_bytes(),
                    ).to_string();
                    let mut attempt = 0_u8;
                    loop {
                        let processing = processor.process(work_configuration.clone(), work_fence.clone(), message.payload.clone(), submission_id.clone());
                        tokio::pin!(processing);
                        let status = loop {
                            tokio::select! {
                                status = &mut processing => break status,
                                _ = tokio::time::sleep(Duration::from_secs(5)) => {
                                    if !work_fence.is_accepting() {
                                        return Ok(());
                                    }
                                    // [COMMENT]: Progress heartbeat chạy cả khi chờ global processor semaphore/JMAP, không chỉ giữa hai retry attempt.
                                    if message.ack_with(AckKind::Progress).await.is_err() {
                                        return Err("MAIL_NATS_JETSTREAM_ACK_FAILED");
                                    }
                                }
                            }
                        };
                        if matches!(status, MailProcessingStatus::Retryable { .. }) && attempt < MAX_SAFE_RETRIES && work_fence.is_accepting() {
                            attempt = attempt.saturating_add(1);
                            tokio::time::sleep(Duration::from_millis((100_u64 << attempt.min(5)) + rand::random::<u64>() % 250)).await;
                            continue;
                        }
                        if !work_fence.is_accepting() {
                            break;
                        }
                        let settled = match status {
                            MailProcessingStatus::Accepted => message.double_ack().await.is_ok(),
                            MailProcessingStatus::PermanentRejected { .. }
                            | MailProcessingStatus::Ambiguous { .. }
                            | MailProcessingStatus::Retryable { .. } => message.ack_with(AckKind::Term).await.is_ok(),
                        };
                        if !settled {
                            return Err("MAIL_NATS_JETSTREAM_ACK_FAILED");
                        }
                        break;
                    }
                    Ok(())
                });
            }
        }
    }

    generation_fence.fence().await;
    tasks.abort_all();
    while tasks.join_next().await.is_some() {}
}
