use super::{RuntimeGenerationFence, StreamRuntimeContext};
use crate::executor::mail::processor::stream::MailProcessingStatus;
use crate::executor::mail::runtime::configuration::RuntimeConsumerConfiguration;
use crate::executor::mail::runtime_proto::{MailStreamType, RabbitMqPayloadV1};
use crate::infra::zone_kv::ZoneLease;
use bytes::Bytes;
use futures_util::StreamExt;
use lapin::options::{BasicAckOptions, BasicConsumeOptions, BasicQosOptions, BasicRejectOptions};
use lapin::types::FieldTable;
use lapin::{Connection, ConnectionProperties};
use prost::Message;
use serde::Deserialize;
use std::sync::Arc;
use std::time::Duration;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use zeroize::Zeroize;

const SUBMISSION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0x44, 0x10, 0xc1, 0x8a, 0xf8, 0x9f, 0x53, 0x82, 0x87, 0x47, 0x52, 0x69, 0xa8, 0x7b, 0x11, 0x7e,
]);
const MAX_SAFE_RETRIES: u8 = 5;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RabbitMqConnectionConfigV1 {
    uri: String,
}

impl Drop for RabbitMqConnectionConfigV1 {
    fn drop(&mut self) {
        self.uri.zeroize();
    }
}

/// [COMMENT]: RabbitMQ suite sở hữu channel/prefetch/delivery-tag ACK; delivery state không đi qua generic coordinator.
pub async fn run(
    context: Arc<StreamRuntimeContext>,
    configuration: Arc<RuntimeConsumerConfiguration>,
    slot: u32,
    generation: u64,
    lease: ZoneLease,
    generation_fence: Arc<RuntimeGenerationFence>,
    cancel: CancellationToken,
) {
    let payload = match RabbitMqPayloadV1::decode(configuration.stream.payload.as_slice()) {
        Ok(payload) if configuration.stream.payload_schema_version == 1 => payload,
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_RABBITMQ_PAYLOAD_INVALID",
                )
                .await;
            return;
        }
    };
    let plaintext = match context.decrypt_connection(
        MailStreamType::Rabbitmq,
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
    let connection_config = match serde_json::from_slice::<RabbitMqConnectionConfigV1>(&plaintext) {
        Ok(connection)
            if connection.uri.starts_with("amqps://")
                && connection.uri.len() <= 4_096
                && !connection.uri.chars().any(char::is_control) =>
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
                    "MAIL_RABBITMQ_CONNECTION_CONFIG_INVALID",
                )
                .await;
            return;
        }
    };
    drop(plaintext);
    let connection = match tokio::time::timeout(
        Duration::from_secs(10),
        Connection::connect(&connection_config.uri, ConnectionProperties::default()),
    )
    .await
    {
        Ok(Ok(connection)) => connection,
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_RABBITMQ_CONNECTION_FAILED",
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
                    "MAIL_RABBITMQ_CONNECTION_FAILED",
                )
                .await;
            return;
        }
    };
    drop(connection_config);
    let channel =
        match tokio::time::timeout(Duration::from_secs(10), connection.create_channel()).await {
            Ok(Ok(channel)) => channel,
            Ok(Err(_)) => {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_RABBITMQ_CHANNEL_FAILED",
                    )
                    .await;
                let _ = connection.close(200, "aurora-close".into()).await;
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
                        "MAIL_RABBITMQ_CHANNEL_FAILED",
                    )
                    .await;
                let _ = connection.close(200, "aurora-close".into()).await;
                return;
            }
        };
    if !matches!(
        tokio::time::timeout(
            Duration::from_secs(10),
            channel.basic_qos(
                context.max_slot_inflight.min(u16::MAX as usize) as u16,
                BasicQosOptions::default(),
            )
        )
        .await,
        Ok(Ok(()))
    ) {
        context
            .write_health(
                "ERROR",
                &configuration,
                slot,
                generation,
                &lease,
                "MAIL_RABBITMQ_QOS_FAILED",
            )
            .await;
        let _ = connection.close(200, "aurora-close".into()).await;
        return;
    }
    let consumer_tag = format!(
        "{}-{}-{slot}",
        payload.consumer_tag_prefix, configuration.consumer_id
    );
    let mut consumer = match tokio::time::timeout(
        Duration::from_secs(10),
        channel.basic_consume(
            payload.queue_name.clone().into(),
            consumer_tag.into(),
            BasicConsumeOptions::default(),
            FieldTable::default(),
        ),
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
                    "MAIL_RABBITMQ_CONSUME_FAILED",
                )
                .await;
            let _ = connection.close(200, "aurora-close".into()).await;
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
                    "MAIL_RABBITMQ_CONSUME_FAILED",
                )
                .await;
            let _ = connection.close(200, "aurora-close".into()).await;
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

    generation_fence.mark_running();
    loop {
        if generation_fence.is_draining() && tasks.is_empty() {
            generation_fence.mark_drained();
            break;
        }
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
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_RABBITMQ_PROCESSING_TASK_FAILED").await;
                        break;
                    }
                }
            }
            next = consumer.next(), if !generation_fence.is_draining() && tasks.len() < context.max_slot_inflight => {
                let Some(next) = next else {
                    context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_RABBITMQ_CONSUMER_CLOSED").await;
                    break;
                };
                let delivery = match next {
                    Ok(delivery) => delivery,
                    Err(_) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_RABBITMQ_CONSUME_FAILED").await;
                        break;
                    }
                };
                if delivery.data.len() > context.max_message_bytes {
                    // [COMMENT]: Rabbit prefetch đã nhận frame nhưng oversized body bị terminal-reject trước khi clone vào task.
                    if delivery.reject(BasicRejectOptions { requeue: false }).await.is_err() {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_RABBITMQ_SETTLEMENT_FAILED").await;
                        break;
                    }
                    continue;
                }
                let Some(message_id) = delivery.properties.message_id().as_ref().map(ToString::to_string) else {
                    // [COMMENT]: Delivery tag không bền qua reconnect; thiếu AMQP message_id thì không tạo idempotency identity giả.
                    if delivery.reject(BasicRejectOptions { requeue: false }).await.is_err() {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_RABBITMQ_SETTLEMENT_FAILED").await;
                        break;
                    }
                    continue;
                };
                let processor = context.processor.clone();
                let work_configuration = configuration.clone();
                let work_fence = generation_fence.clone();
                tasks.spawn(async move {
                    let submission_id = uuid::Uuid::new_v5(
                        &SUBMISSION_NAMESPACE,
                        format!("{}\0rabbitmq\0{}", work_configuration.consumer_id, message_id).as_bytes(),
                    ).to_string();
                    let message_payload = Bytes::from(delivery.data.clone());
                    let mut attempt = 0_u8;
                    loop {
                        let status = processor.process(work_configuration.clone(), work_fence.clone(), message_payload.clone(), submission_id.clone()).await;
                        if matches!(status, MailProcessingStatus::Retryable { .. }) && attempt < MAX_SAFE_RETRIES && work_fence.is_accepting() {
                            attempt = attempt.saturating_add(1);
                            tokio::time::sleep(Duration::from_millis((100_u64 << attempt.min(5)) + rand::random::<u64>() % 250)).await;
                            continue;
                        }
                        if !work_fence.is_accepting() {
                            break;
                        }
                        let settled = match status {
                            MailProcessingStatus::Accepted => delivery.ack(BasicAckOptions::default()).await,
                            MailProcessingStatus::PermanentRejected { .. }
                            | MailProcessingStatus::Ambiguous { .. }
                            | MailProcessingStatus::Retryable { .. } => delivery.reject(BasicRejectOptions { requeue: false }).await,
                        };
                        if settled.is_err() {
                            return Err("MAIL_RABBITMQ_SETTLEMENT_FAILED");
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
    if connection.close(200, "aurora-close".into()).await.is_err() {
        generation_fence.mark_running();
    }
}
