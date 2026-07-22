use super::{RuntimeGenerationFence, StreamRuntimeContext};
use crate::executor::mail::runtime_configuration::RuntimeConsumerConfiguration;
use crate::executor::mail::runtime_proto::{MailStreamType, RedisStreamPayloadV1};
use crate::executor::mail::stream_processor::MailProcessingStatus;
use crate::infra::zone_kv::ZoneLease;
use bytes::Bytes;
use prost::Message;
use redis::streams::{StreamReadOptions, StreamReadReply};
use redis::{AsyncCommands, FromRedisValue, Value};
use serde::Deserialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use zeroize::Zeroize;

const SUBMISSION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0xe8, 0x21, 0x53, 0x18, 0x40, 0xf1, 0x57, 0xd8, 0x9c, 0x2f, 0x90, 0x8c, 0x4d, 0xc3, 0xb8, 0x11,
]);
const MAX_SAFE_RETRIES: u8 = 5;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RedisStreamConnectionConfigV1 {
    url: String,
}

impl Drop for RedisStreamConnectionConfigV1 {
    fn drop(&mut self) {
        self.url.zeroize();
    }
}

struct RedisStreamWork {
    entry_id: String,
    payload: Bytes,
}

type RedisClaimReply = (String, Vec<(String, HashMap<String, Value>)>, Vec<String>);

/// [COMMENT]: Redis suite dùng PEL/XAUTOCLAIM/XACK native; không có Kafka watermark hoặc partition coordinator.
pub async fn run(
    context: Arc<StreamRuntimeContext>,
    configuration: Arc<RuntimeConsumerConfiguration>,
    slot: u32,
    generation: u64,
    lease: ZoneLease,
    generation_fence: Arc<RuntimeGenerationFence>,
    cancel: CancellationToken,
) {
    let payload = match RedisStreamPayloadV1::decode(configuration.stream.payload.as_slice()) {
        Ok(payload) if configuration.stream.payload_schema_version == 1 => payload,
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_REDIS_STREAM_PAYLOAD_INVALID",
                )
                .await;
            return;
        }
    };
    let plaintext = match context.decrypt_connection(
        MailStreamType::RedisStream,
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
    let connection = match serde_json::from_slice::<RedisStreamConnectionConfigV1>(&plaintext) {
        Ok(connection)
            if connection.url.starts_with("rediss://")
                && connection.url.len() <= 4_096
                && !connection.url.chars().any(char::is_control) =>
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
                    "MAIL_REDIS_STREAM_CONNECTION_CONFIG_INVALID",
                )
                .await;
            return;
        }
    };
    drop(plaintext);
    let client = match redis::Client::open(connection.url.as_str()) {
        Ok(client) => client,
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_REDIS_STREAM_CONNECTION_FAILED",
                )
                .await;
            return;
        }
    };
    // [COMMENT]: Redis client giữ transport config cần thiết; bản plaintext tạm được zeroize ngay sau parse/open.
    drop(connection);
    let mut manager = match tokio::time::timeout(
        Duration::from_secs(10),
        client.get_connection_manager(),
    )
    .await
    {
        Ok(Ok(manager)) => manager,
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_REDIS_STREAM_CONNECTION_FAILED",
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
                    "MAIL_REDIS_STREAM_CONNECTION_FAILED",
                )
                .await;
            return;
        }
    };
    // [COMMENT]: Group create là idempotent; BUSYGROUP nghĩa group đã sẵn sàng, lỗi khác cô lập đúng consumer này.
    let create_group: redis::RedisResult<Value> = match tokio::time::timeout(
        Duration::from_secs(10),
        redis::cmd("XGROUP")
            .arg("CREATE")
            .arg(&payload.stream_key)
            .arg(&payload.consumer_group)
            .arg("0")
            .arg("MKSTREAM")
            .query_async(&mut manager),
    )
    .await
    {
        Ok(result) => result,
        Err(_) => Err(redis::RedisError::from((
            redis::ErrorKind::IoError,
            "mail Redis Stream group create timed out",
        ))),
    };
    if create_group
        .as_ref()
        .err()
        .is_some_and(|error| !error.to_string().contains("BUSYGROUP"))
    {
        context
            .write_health(
                "ERROR",
                &configuration,
                slot,
                generation,
                &lease,
                "MAIL_REDIS_STREAM_GROUP_UNAVAILABLE",
            )
            .await;
        return;
    }

    context
        .write_health("RUNNING", &configuration, slot, generation, &lease, "")
        .await;
    let consumer_name = format!(
        "aurora-{}-{}-{slot}",
        context.instance_id, configuration.consumer_id
    );
    let renew_every = (context.lease_ttl / 3).max(Duration::from_secs(1));
    let mut renew = tokio::time::interval(renew_every);
    renew.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut reclaim = tokio::time::interval(Duration::from_secs(30));
    reclaim.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut tasks = JoinSet::<Result<(), &'static str>>::new();
    let stream_keys = [payload.stream_key.as_str()];
    let read_ids = [">"];
    let read_batch_size = context.max_slot_inflight.clamp(1, 64);
    let read_options = StreamReadOptions::default()
        .group(&payload.consumer_group, &consumer_name)
        .count(read_batch_size)
        .block(500);

    'runtime: loop {
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
                        break 'runtime;
                    }
                    Some(Err(_)) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_PROCESSING_TASK_FAILED").await;
                        break 'runtime;
                    }
                }
            }
            _ = reclaim.tick(), if tasks.len().saturating_add(read_batch_size) <= context.max_slot_inflight => {
                // [COMMENT]: PEL của pod chết được claim theo batch nhỏ; entry chưa terminal không biến mất sau restart.
                let claimed: redis::RedisResult<RedisClaimReply> = redis::cmd("XAUTOCLAIM")
                    .arg(&payload.stream_key)
                    .arg(&payload.consumer_group)
                    .arg(&consumer_name)
                    .arg(60_000_u64)
                    .arg("0-0")
                    .arg("COUNT")
                    .arg(read_batch_size)
                    .query_async(&mut manager)
                    .await;
                if let Ok((_, entries, _)) = claimed {
                    for (entry_id, fields) in entries {
                        let Some(value) = fields.get("payload") else {
                            // [COMMENT]: Entry sai transport contract không thể tự hồi phục; ACK terminal để không tạo poison PEL vô hạn.
                            let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry_id.as_str()]).await;
                            if !matches!(acked, Ok(1..)) {
                                context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                break 'runtime;
                            }
                            continue;
                        };
                        let Ok(message_payload) = Vec::<u8>::from_redis_value(value) else {
                            let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry_id.as_str()]).await;
                            if !matches!(acked, Ok(1..)) {
                                context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                break 'runtime;
                            }
                            continue;
                        };
                        if message_payload.len() > context.max_message_bytes {
                            // [COMMENT]: Không giữ oversized Redis value trong JoinSet; transport đã allocate nhưng terminal-ACK ngay để PEL không phình.
                            let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry_id.as_str()]).await;
                            if !matches!(acked, Ok(1..)) {
                                context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                break 'runtime;
                            }
                            continue;
                        }
                        let work = RedisStreamWork { entry_id, payload: Bytes::from(message_payload) };
                        let work_context = context.clone();
                        let work_configuration = configuration.clone();
                        let work_fence = generation_fence.clone();
                        let mut work_manager = manager.clone();
                        let stream_key = payload.stream_key.clone();
                        let consumer_group = payload.consumer_group.clone();
                        let redis_consumer_name = consumer_name.clone();
                        tasks.spawn(async move {
                            let submission_id = uuid::Uuid::new_v5(
                                &SUBMISSION_NAMESPACE,
                                format!("{}\0redis-stream\0{}\0{}", work_configuration.consumer_id, stream_key, work.entry_id).as_bytes(),
                            ).to_string();
                            let mut attempt = 0_u8;
                            loop {
                                let processing = work_context.processor.process(
                                    work_configuration.clone(), work_fence.clone(), work.payload.clone(), submission_id.clone(),
                                );
                                tokio::pin!(processing);
                                let status = loop {
                                    tokio::select! {
                                        status = &mut processing => break status,
                                        _ = tokio::time::sleep(Duration::from_secs(15)) => {
                                            if !work_fence.is_accepting() {
                                                return Ok(());
                                            }
                                            // [COMMENT]: Reset PEL idle trong lúc chờ semaphore/JMAP để XAUTOCLAIM không chạy song song cùng message còn sống.
                                            let touched = tokio::time::timeout(
                                                Duration::from_secs(5),
                                                redis::cmd("XCLAIM")
                                                    .arg(&stream_key).arg(&consumer_group).arg(&redis_consumer_name)
                                                    .arg(0_u64).arg(&work.entry_id).arg("JUSTID")
                                                    .query_async::<_, Vec<String>>(&mut work_manager),
                                            ).await;
                                            if !matches!(touched, Ok(Ok(ref ids)) if ids.iter().any(|id| id == &work.entry_id)) {
                                                return Err("MAIL_REDIS_STREAM_CLAIM_HEARTBEAT_FAILED");
                                            }
                                        }
                                    }
                                };
                                if matches!(status, MailProcessingStatus::Retryable { .. })
                                    && attempt < MAX_SAFE_RETRIES
                                    && work_fence.is_accepting()
                                {
                                    attempt = attempt.saturating_add(1);
                                    tokio::time::sleep(Duration::from_millis((100_u64 << attempt.min(5)) + rand::random::<u64>() % 250)).await;
                                    continue;
                                }
                                // [COMMENT]: Không ACK sau fence; PEL giữ entry cho owner mới XAUTOCLAIM.
                                if work_fence.is_accepting() {
                                    let acked: redis::RedisResult<u64> = work_manager.xack(&stream_key, &consumer_group, &[work.entry_id.as_str()]).await;
                                    match acked {
                                        Ok(1..) => {}
                                        _ => return Err("MAIL_REDIS_STREAM_ACK_FAILED"),
                                    }
                                }
                                break;
                            }
                            Ok(())
                        });
                    }
                }
            }
            result = manager.xread_options::<_, _, StreamReadReply>(
                &stream_keys,
                &read_ids,
                &read_options,
            ), if tasks.len().saturating_add(read_batch_size) <= context.max_slot_inflight => {
                match result {
                    Ok(reply) => {
                        for key in reply.keys {
                            for entry in key.ids {
                                let Some(value) = entry.map.get("payload") else {
                                    let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry.id.as_str()]).await;
                                    if !matches!(acked, Ok(1..)) {
                                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                        break 'runtime;
                                    }
                                    continue;
                                };
                                let Ok(message_payload) = Vec::<u8>::from_redis_value(value) else {
                                    let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry.id.as_str()]).await;
                                    if !matches!(acked, Ok(1..)) {
                                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                        break 'runtime;
                                    }
                                    continue;
                                };
                                if message_payload.len() > context.max_message_bytes {
                                    let acked: redis::RedisResult<u64> = manager.xack(&payload.stream_key, &payload.consumer_group, &[entry.id.as_str()]).await;
                                    if !matches!(acked, Ok(1..)) {
                                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_ACK_FAILED").await;
                                        break 'runtime;
                                    }
                                    continue;
                                }
                                let work = RedisStreamWork { entry_id: entry.id, payload: Bytes::from(message_payload) };
                                let work_context = context.clone();
                                let work_configuration = configuration.clone();
                                let work_fence = generation_fence.clone();
                                let mut work_manager = manager.clone();
                                let stream_key = payload.stream_key.clone();
                                let consumer_group = payload.consumer_group.clone();
                                let redis_consumer_name = consumer_name.clone();
                                tasks.spawn(async move {
                                    let submission_id = uuid::Uuid::new_v5(
                                        &SUBMISSION_NAMESPACE,
                                        format!("{}\0redis-stream\0{}\0{}", work_configuration.consumer_id, stream_key, work.entry_id).as_bytes(),
                                    ).to_string();
                                    let mut attempt = 0_u8;
                                    loop {
                                        let processing = work_context.processor.process(
                                            work_configuration.clone(), work_fence.clone(), work.payload.clone(), submission_id.clone(),
                                        );
                                        tokio::pin!(processing);
                                        let status = loop {
                                            tokio::select! {
                                                status = &mut processing => break status,
                                                _ = tokio::time::sleep(Duration::from_secs(15)) => {
                                                    if !work_fence.is_accepting() {
                                                        return Ok(());
                                                    }
                                                    // [COMMENT]: New-entry path cũng heartbeat đúng PEL owner; không giả định JMAP luôn xong dưới min-idle.
                                                    let touched = tokio::time::timeout(
                                                        Duration::from_secs(5),
                                                        redis::cmd("XCLAIM")
                                                            .arg(&stream_key).arg(&consumer_group).arg(&redis_consumer_name)
                                                            .arg(0_u64).arg(&work.entry_id).arg("JUSTID")
                                                            .query_async::<_, Vec<String>>(&mut work_manager),
                                                    ).await;
                                                    if !matches!(touched, Ok(Ok(ref ids)) if ids.iter().any(|id| id == &work.entry_id)) {
                                                        return Err("MAIL_REDIS_STREAM_CLAIM_HEARTBEAT_FAILED");
                                                    }
                                                }
                                            }
                                        };
                                        if matches!(status, MailProcessingStatus::Retryable { .. })
                                            && attempt < MAX_SAFE_RETRIES
                                            && work_fence.is_accepting()
                                        {
                                            attempt = attempt.saturating_add(1);
                                            tokio::time::sleep(Duration::from_millis((100_u64 << attempt.min(5)) + rand::random::<u64>() % 250)).await;
                                            continue;
                                        }
                                        // [COMMENT]: New-entry path dùng đúng PEL settlement như reclaim path nhưng không qua generic helper.
                                        if work_fence.is_accepting() {
                                            let acked: redis::RedisResult<u64> = work_manager.xack(&stream_key, &consumer_group, &[work.entry_id.as_str()]).await;
                                            match acked {
                                                Ok(1..) => {}
                                                _ => return Err("MAIL_REDIS_STREAM_ACK_FAILED"),
                                            }
                                        }
                                        break;
                                    }
                                    Ok(())
                                });
                            }
                        }
                    }
                    Err(_) => {
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_REDIS_STREAM_READ_FAILED").await;
                        break 'runtime;
                    }
                }
            }
        }
    }

    generation_fence.fence().await;
    tasks.abort_all();
    while tasks.join_next().await.is_some() {}
}
