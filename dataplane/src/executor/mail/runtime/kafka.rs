use super::{RuntimeGenerationFence, StreamRuntimeContext};
use crate::executor::mail::processor::stream::MailProcessingStatus;
use crate::executor::mail::runtime::configuration::RuntimeConsumerConfiguration;
use crate::executor::mail::runtime_proto::{KafkaStreamPayloadV1, MailStreamType};
use crate::infra::zone_kv::ZoneLease;
use ahash::AHashMap;
use bytes::Bytes;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{
    AutoOffsetReset, Consumer, ConsumerRebalanceListener, OffsetAndMetadata, TopicPartition,
};
use prost::Message;
use serde::Deserialize;
use std::collections::{BTreeMap, BTreeSet};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use zeroize::Zeroize;

const SUBMISSION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0x26, 0xb8, 0x56, 0x10, 0x69, 0x6c, 0x59, 0xb4, 0x9a, 0xf4, 0xf0, 0x8b, 0x45, 0x51, 0xa5, 0x38,
]);
const MAX_SAFE_RETRIES: u8 = 5;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaConnectionConfigV1 {
    bootstrap_servers: String,
    security_protocol: String,
    #[serde(default)]
    username: Option<String>,
    #[serde(default)]
    password: Option<String>,
}

impl Drop for KafkaConnectionConfigV1 {
    fn drop(&mut self) {
        self.bootstrap_servers.zeroize();
        self.security_protocol.zeroize();
        self.username.zeroize();
        self.password.zeroize();
    }
}

#[derive(Default)]
struct KafkaRebalanceFence {
    epoch: AtomicU64,
}

impl ConsumerRebalanceListener for KafkaRebalanceFence {
    async fn on_partitions_assigned(&self, _partitions: &[TopicPartition]) {
        // [COMMENT]: Mọi assignment round đều fence completion cũ, kể cả partition quay lại cùng pod.
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_revoked(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_lost(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }
}

#[derive(Clone)]
struct KafkaWork {
    topic: String,
    partition: i32,
    offset: i64,
    payload: Bytes,
    assignment_epoch: u64,
}

struct KafkaCompletion {
    work: KafkaWork,
    terminal: bool,
}

struct KafkaPartitionSettlement {
    next_terminal_offset: i64,
    terminal: BTreeSet<i64>,
    committed_next_offset: i64,
}

/// [COMMENT]: Kafka suite sở hữu toàn bộ group/rebalance/offset state; processor chung không thấy Kafka coordinate.
pub async fn run(
    context: Arc<StreamRuntimeContext>,
    configuration: Arc<RuntimeConsumerConfiguration>,
    slot: u32,
    generation: u64,
    lease: ZoneLease,
    generation_fence: Arc<RuntimeGenerationFence>,
    cancel: CancellationToken,
) {
    let payload = match KafkaStreamPayloadV1::decode(configuration.stream.payload.as_slice()) {
        Ok(payload) if configuration.stream.payload_schema_version == 1 => payload,
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_KAFKA_PAYLOAD_INVALID",
                )
                .await;
            return;
        }
    };
    let plaintext = match context.decrypt_connection(
        MailStreamType::Kafka,
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
    let connection = match serde_json::from_slice::<KafkaConnectionConfigV1>(&plaintext) {
        Ok(connection)
            if !connection.bootstrap_servers.trim().is_empty()
                && connection.bootstrap_servers.len() <= 4_096
                && connection.bootstrap_servers.split(',').count() <= 32
                && !connection.bootstrap_servers.chars().any(char::is_control) =>
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
                    "MAIL_KAFKA_CONNECTION_CONFIG_INVALID",
                )
                .await;
            return;
        }
    };
    // [COMMENT]: Raw decrypted JSON không cần sống xuyên network await; typed credential struct sẽ tự zeroize sau khi builder xong.
    drop(plaintext);

    let rebalance = Arc::new(KafkaRebalanceFence::default());
    let poll_batch_size = context.max_slot_inflight.clamp(1, 64);
    let mut builder = Consumer::builder()
        .bootstrap_servers(connection.bootstrap_servers.clone())
        .group_id(payload.consumer_group.clone())
        .client_id(format!("aurora-mail-{}-{slot}", configuration.consumer_id))
        .group_instance_id(format!("aurora-{}-{slot}", configuration.consumer_id))
        .rebalance_listener(rebalance.clone())
        .auto_offset_reset(AutoOffsetReset::Earliest)
        .enable_auto_commit(false)
        .max_poll_records(poll_batch_size as i32)
        .max_partition_fetch_bytes(context.max_message_bytes.min(i32::MAX as usize) as i32)
        .fetch_max_bytes(
            context
                .max_message_bytes
                .saturating_mul(poll_batch_size)
                .min(i32::MAX as usize) as i32,
        )
        .request_timeout(Duration::from_secs(10));
    let mut tls = TlsConfig::new().with_native_roots();
    if let Some(ca_path) = &crate::config::Config::get_global().mail_stream_ca_cert_path {
        tls = tls.with_ca_cert(ca_path);
    }
    builder = match connection.security_protocol.as_str() {
        "plaintext" if crate::config::Config::get_global().mail_stream_allow_plaintext_kafka => {
            builder
        }
        "ssl" => builder.auth(AuthConfig::ssl(tls)),
        "sasl_plain_ssl" => {
            let (Some(username), Some(password)) =
                (connection.username.as_ref(), connection.password.as_ref())
            else {
                context
                    .write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_KAFKA_CREDENTIAL_REQUIRED",
                    )
                    .await;
                return;
            };
            match AuthConfig::sasl_plain_ssl(username, password, tls) {
                Ok(auth) => builder.auth(auth),
                Err(_) => {
                    context
                        .write_health(
                            "ERROR",
                            &configuration,
                            slot,
                            generation,
                            &lease,
                            "MAIL_KAFKA_CREDENTIAL_INVALID",
                        )
                        .await;
                    return;
                }
            }
        }
        _ => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_KAFKA_SECURITY_PROTOCOL_UNSUPPORTED",
                )
                .await;
            return;
        }
    };
    let consumer = match tokio::time::timeout(Duration::from_secs(15), builder.build()).await {
        Ok(Ok(consumer)) => consumer,
        Err(_) => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_KAFKA_CONNECTION_FAILED",
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
                    "MAIL_KAFKA_CONNECTION_FAILED",
                )
                .await;
            return;
        }
    };
    drop(connection);
    if !matches!(
        tokio::time::timeout(
            Duration::from_secs(10),
            consumer.subscribe(&[payload.topic.as_str()])
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
                "MAIL_KAFKA_SUBSCRIBE_FAILED",
            )
            .await;
        let _ = consumer.close().await;
        return;
    }
    context
        .write_health("RUNNING", &configuration, slot, generation, &lease, "")
        .await;

    let renew_every = (context.lease_ttl / 3).max(Duration::from_secs(1));
    let mut renew = tokio::time::interval(renew_every);
    renew.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    // [COMMENT]: Hai commit attempt/giây đủ giữ lag thấp nhưng không tạo retry storm khi customer Kafka down.
    let mut commit_tick = tokio::time::interval(Duration::from_millis(500));
    commit_tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut tasks = JoinSet::<KafkaCompletion>::new();
    let mut settlement = BTreeMap::<(String, i32), KafkaPartitionSettlement>::new();
    let mut dirty = AHashMap::<TopicPartition, OffsetAndMetadata>::new();
    let mut observed_epoch = rebalance.epoch.load(Ordering::Acquire);
    let mut consecutive_poll_errors = 0_u8;
    let mut lease_is_current = true;

    'runtime: loop {
        let current_epoch = rebalance.epoch.load(Ordering::Acquire);
        if current_epoch != observed_epoch {
            // [COMMENT]: State của assignment cũ không được dùng sau rebalance; Kafka sẽ redeliver từ committed offset.
            observed_epoch = current_epoch;
            settlement.clear();
            dirty.clear();
        }
        tokio::select! {
            _ = cancel.cancelled() => break,
            _ = renew.tick() => {
                if !context.renew_and_report(&configuration, slot, generation, &lease, &generation_fence).await {
                    // [COMMENT]: Sau khi mất Zone lease, owner cũ tuyệt đối không được commit offset dù work vừa hoàn tất.
                    lease_is_current = false;
                    break;
                }
            }
            _ = commit_tick.tick(), if !dirty.is_empty() => {
                if !generation_fence.is_accepting() {
                    // [COMMENT]: Local monotonic deadline có thể hết trước renew branch; không commit trong khe đó.
                    lease_is_current = false;
                    break;
                }
                if consumer.commit_with_metadata(dirty.clone()).await.is_ok() {
                    for (topic_partition, committed) in dirty.drain() {
                        if let Some(state) = settlement.get_mut(&(topic_partition.topic, topic_partition.partition)) {
                            state.committed_next_offset = committed.offset;
                        }
                    }
                }
            }
            completed = tasks.join_next(), if !tasks.is_empty() => {
                let Some(completed) = completed else { continue };
                let completion = match completed {
                    Ok(completion) => completion,
                    Err(_) => {
                        // [COMMENT]: Panic/cancel làm mất coordinate result; đóng session để Kafka redeliver từ committed watermark.
                        context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_KAFKA_PROCESSING_TASK_FAILED").await;
                        break 'runtime;
                    }
                };
                if !completion.terminal {
                    continue;
                }
                if completion.work.assignment_epoch != rebalance.epoch.load(Ordering::Acquire) {
                    continue;
                }
                let key = (completion.work.topic.clone(), completion.work.partition);
                let state = settlement.entry(key.clone()).or_insert_with(|| KafkaPartitionSettlement {
                    next_terminal_offset: completion.work.offset,
                    terminal: BTreeSet::new(),
                    committed_next_offset: completion.work.offset,
                });
                // [COMMENT]: Accepted, permanent reject, ambiguous và exhausted retry đều terminal theo policy bulk-mail hiện tại.
                state.terminal.insert(completion.work.offset);
                while state.terminal.remove(&state.next_terminal_offset) {
                    state.next_terminal_offset = state.next_terminal_offset.saturating_add(1);
                }
                if state.next_terminal_offset > state.committed_next_offset {
                    dirty.insert(
                        TopicPartition::new(key.0, key.1),
                        OffsetAndMetadata::with_metadata(
                            state.next_terminal_offset,
                            format!("aurora:{generation}:{}", lease.fencing_token),
                        ),
                    );
                }
            }
            result = consumer.poll(Duration::from_millis(500)), if tasks.len().saturating_add(poll_batch_size) <= context.max_slot_inflight => {
                let records = match result {
                    Ok(records) => {
                        consecutive_poll_errors = 0;
                        records
                    }
                    Err(_) => {
                        consecutive_poll_errors = consecutive_poll_errors.saturating_add(1);
                        if consecutive_poll_errors >= 5 {
                            context.write_health("ERROR", &configuration, slot, generation, &lease, "MAIL_KAFKA_POLL_FAILED").await;
                            break 'runtime;
                        }
                        continue;
                    }
                };
                for record in records {
                    let work = KafkaWork {
                        topic: record.topic,
                        partition: record.partition,
                        offset: record.offset,
                        payload: record.value.unwrap_or_default(),
                        assignment_epoch: rebalance.epoch.load(Ordering::Acquire),
                    };
                    let key = (work.topic.clone(), work.partition);
                    settlement.entry(key).or_insert_with(|| KafkaPartitionSettlement {
                        next_terminal_offset: work.offset,
                        terminal: BTreeSet::new(),
                        committed_next_offset: work.offset,
                    });
                    let processor = context.processor.clone();
                    let work_configuration = configuration.clone();
                    let work_fence = generation_fence.clone();
                    tasks.spawn(async move {
                        let submission_id = uuid::Uuid::new_v5(
                            &SUBMISSION_NAMESPACE,
                            format!("{}\0kafka\0{}\0{}\0{}", work_configuration.consumer_id, work.topic, work.partition, work.offset).as_bytes(),
                        ).to_string();
                        let mut attempt = 0_u8;
                        loop {
                            let status = processor.process(work_configuration.clone(), work_fence.clone(), work.payload.clone(), submission_id.clone()).await;
                            if matches!(status, MailProcessingStatus::Retryable { .. })
                                && attempt < MAX_SAFE_RETRIES
                                && work_fence.is_accepting()
                            {
                                attempt = attempt.saturating_add(1);
                                let jitter = rand::random::<u64>() % 250;
                                tokio::time::sleep(Duration::from_millis((100_u64 << attempt.min(5)) + jitter)).await;
                                continue;
                            }
                            // [COMMENT]: Retryable do generation vừa bị fence phải để Kafka redeliver, không được biến thành terminal offset.
                            let terminal = !matches!(status, MailProcessingStatus::Retryable { .. })
                                || work_fence.is_accepting();
                            return KafkaCompletion { work, terminal };
                        }
                    });
                }
            }
        }
    }

    generation_fence.fence().await;
    while let Some(completed) = tasks.join_next().await {
        if let Ok(completion) = completed {
            // [COMMENT]: Drain chỉ giữ completion cùng assignment epoch; stale work để broker redeliver.
            if completion.terminal
                && completion.work.assignment_epoch == rebalance.epoch.load(Ordering::Acquire)
            {
                let key = (completion.work.topic, completion.work.partition);
                let state =
                    settlement
                        .entry(key.clone())
                        .or_insert_with(|| KafkaPartitionSettlement {
                            next_terminal_offset: completion.work.offset,
                            terminal: BTreeSet::new(),
                            committed_next_offset: completion.work.offset,
                        });
                state.terminal.insert(completion.work.offset);
                while state.terminal.remove(&state.next_terminal_offset) {
                    state.next_terminal_offset = state.next_terminal_offset.saturating_add(1);
                }
                if state.next_terminal_offset > state.committed_next_offset {
                    dirty.insert(
                        TopicPartition::new(key.0, key.1),
                        OffsetAndMetadata::with_metadata(
                            state.next_terminal_offset,
                            format!("aurora:{generation}:{}", lease.fencing_token),
                        ),
                    );
                }
            }
        }
    }
    if lease_is_current
        && generation_fence.lease_is_live()
        && observed_epoch == rebalance.epoch.load(Ordering::Acquire)
        && !dirty.is_empty()
    {
        let _ = consumer.commit_with_metadata(dirty).await;
    }
    let _ = consumer.close().await;
}
