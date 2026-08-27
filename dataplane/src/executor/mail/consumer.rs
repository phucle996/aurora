use super::runtime_proto::{
    KafkaStreamPayloadV1, MailConsumerDeleteV1, MailConsumerDrainV1, MailConsumerDrainedV1,
    MailConsumerUpsertV1, MailStreamType, NatsJetStreamPayloadV1, RabbitMqPayloadV1,
    RedisStreamPayloadV1,
};
use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::zone_kv::{ConsumerConfigHead, ZoneKvStore};
use crate::job_runtime::model::ValidatedJob;
use bytes::Bytes;
use prost::Message;
use serde::Serialize;
use std::sync::Arc;
use std::time::Duration;

const CONFIG_CONSUMER_HEAD_PREFIX: &str = "mail.consumer.head.";
const CONFIG_CONSUMER_SNAPSHOT_PREFIX: &str = "mail.consumer.snapshot.";
#[cfg(test)]
#[path = "test/consumer.rs"]
mod tests;
#[derive(Serialize)]
struct ConsumerHeadWrite<'a> {
    schema_version: u32,
    runtime_read_enabled: bool,
    module: &'a str,
    resource_type: &'a str,
    resource_id: &'a str,
    version: u64,
    event_id: &'a str,
    config_sha256: &'a str,
    desired_state: &'a str,
    tombstoned: bool,
    owner_id: &'a str,
    owner_type: &'a str,
    workspace_id: &'a str,
    zone_id: &'a str,
    runtime_protocol: u32,
    runtime_generations: &'a [String],
}

fn consumer_head_key(id: &str) -> String {
    format!("{CONFIG_CONSUMER_HEAD_PREFIX}{id}")
}

fn consumer_snapshot_key(id: &str, version: u64) -> String {
    format!("{CONFIG_CONSUMER_SNAPSHOT_PREFIX}{id}.v{version}")
}

async fn ensure_immutable_snapshot(
    kv: &ZoneKvStore,
    key: &str,
    payload: &[u8],
) -> Result<bool, String> {
    if let Some(existing) = kv.config_get(key.to_string()).await? {
        if existing.as_ref() == payload {
            return Ok(false);
        }
        return Err(format!("immutable snapshot conflict for {key}"));
    }
    match kv.config_create(key, Bytes::copy_from_slice(payload)).await {
        Ok(_) => Ok(true),
        Err(_) => {
            let existing = kv
                .config_get(key.to_string())
                .await?
                .ok_or_else(|| format!("snapshot create raced and value is missing for {key}"))?;
            if existing.as_ref() == payload {
                Ok(false)
            } else {
                Err(format!("immutable snapshot conflict for {key}"))
            }
        }
    }
}

fn event_id(
    payload: &Arc<ValidatedJob>,
    metadata: &super::runtime_proto::MailEventMetadataV1,
) -> Result<String, ExecutorError> {
    let job_uuid = uuid::Uuid::parse_str(&payload.job_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_EVENT_ID_INVALID".to_string()))?;
    if metadata.schema_version != 1 || metadata.event_id.as_slice() != job_uuid.as_bytes() {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_EVENT_METADATA_INVALID".to_string(),
        ));
    }
    Ok(payload.job_id.clone())
}

pub async fn apply_mail_consumer_upsert(
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    if payload.payload_schema_version != 1 {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_SCHEMA_UNSUPPORTED".to_string(),
        ));
    }
    let event = MailConsumerUpsertV1::decode(payload.payload.as_ref()).map_err(|error| {
        ExecutorError::ExecutionFailed(format!("MAIL_CONSUMER_UPSERT_DECODE: {error}"))
    })?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    let stream = event.stream.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_CONSUMER_STREAM_REQUIRED".to_string())
    })?;
    if event.consumer_id.len() != 16
        || event.config_version == 0
        || event.config_sha256.len() != 32
        || !matches!(
            stream.stream_type,
            value if value == MailStreamType::Kafka as i32
                || value == MailStreamType::RedisStream as i32
                || value == MailStreamType::NatsJetstream as i32
                || value == MailStreamType::Rabbitmq as i32
        )
        || stream.payload_schema_version == 0
        || stream.broker_resource_id.len() != 16
        || stream.payload.len() > 32 << 10
        || (event.desired_state == 2 && stream.payload.is_empty())
        || event.template_id.trim().is_empty()
        || uuid::Uuid::parse_str(&event.template_id).is_err()
        || event.template_version == 0
        || event.sender_profile_id.trim().is_empty()
        || event.sender_version == 0
        || !matches!(event.desired_state, 1 | 2)
        || event.parallelism == 0
        || uuid::Uuid::from_slice(&event.owner_id).is_err()
        || !matches!(event.owner_type.as_str(), "PERSONAL" | "TENANT")
        || uuid::Uuid::from_slice(&event.workspace_id).is_err()
        || uuid::Uuid::from_slice(&event.zone_id).is_err()
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_CONSUMER_UPSERT_INVALID".to_string(),
        ));
    }
    // [COMMENT]: Projection validate đúng protobuf của suite trước khi ghi immutable KV; không để payload lỗi chờ tới hot path mới phát hiện.
    let source_valid = match stream.stream_type {
        value if value == MailStreamType::Kafka as i32 && stream.payload_schema_version == 1 => {
            KafkaStreamPayloadV1::decode(stream.payload.as_slice())
                .ok()
                .is_some_and(|payload| {
                    payload.source_config_envelope.len() <= 16 << 10
                        && (event.desired_state != 2 || !payload.source_config_envelope.is_empty())
                        && !payload.topic.is_empty()
                        && payload.topic.len() <= 249
                        && payload.topic.bytes().all(|byte| {
                            byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-')
                        })
                        && !payload.consumer_group.trim().is_empty()
                        && payload.consumer_group.len() <= 255
                        && !payload.consumer_group.chars().any(char::is_control)
                })
        }
        value
            if value == MailStreamType::RedisStream as i32
                && stream.payload_schema_version == 1 =>
        {
            RedisStreamPayloadV1::decode(stream.payload.as_slice())
                .ok()
                .is_some_and(|payload| {
                    payload.source_config_envelope.len() <= 16 << 10
                        && (event.desired_state != 2 || !payload.source_config_envelope.is_empty())
                        && !payload.stream_key.trim().is_empty()
                        && payload.stream_key.len() <= 512
                        && !payload.stream_key.chars().any(char::is_control)
                        && !payload.consumer_group.trim().is_empty()
                        && payload.consumer_group.len() <= 255
                        && !payload.consumer_group.chars().any(char::is_control)
                })
        }
        value
            if value == MailStreamType::NatsJetstream as i32
                && stream.payload_schema_version == 1 =>
        {
            NatsJetStreamPayloadV1::decode(stream.payload.as_slice())
                .ok()
                .is_some_and(|payload| {
                    payload.source_config_envelope.len() <= 16 << 10
                        && (event.desired_state != 2 || !payload.source_config_envelope.is_empty())
                        && !payload.stream_name.trim().is_empty()
                        && payload.stream_name.len() <= 255
                        && !payload.stream_name.chars().any(char::is_control)
                        && !payload.durable_name.trim().is_empty()
                        && payload.durable_name.len() <= 255
                        && !payload.durable_name.chars().any(char::is_control)
                })
        }
        value if value == MailStreamType::Rabbitmq as i32 && stream.payload_schema_version == 1 => {
            RabbitMqPayloadV1::decode(stream.payload.as_slice())
                .ok()
                .is_some_and(|payload| {
                    payload.source_config_envelope.len() <= 16 << 10
                        && (event.desired_state != 2 || !payload.source_config_envelope.is_empty())
                        && !payload.queue_name.trim().is_empty()
                        && payload.queue_name.len() <= 255
                        && !payload.queue_name.chars().any(char::is_control)
                        && !payload.consumer_tag_prefix.trim().is_empty()
                        && payload.consumer_tag_prefix.len() <= 128
                        && !payload.consumer_tag_prefix.chars().any(char::is_control)
                })
        }
        _ => false,
    };
    if !source_valid {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_CONSUMER_STREAM_PAYLOAD_INVALID".to_string(),
        ));
    }

    let consumer_id = uuid::Uuid::from_slice(&event.consumer_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_CONSUMER_ID_INVALID".to_string()))?
        .to_string();
    let owner_id = uuid::Uuid::from_slice(&event.owner_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_OWNER_ID_INVALID".to_string()))?
        .to_string();
    let workspace_id = uuid::Uuid::from_slice(&event.workspace_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_WORKSPACE_ID_INVALID".to_string()))?
        .to_string();
    let zone_id = uuid::Uuid::from_slice(&event.zone_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_ZONE_ID_INVALID".to_string()))?
        .to_string();
    if zone_id != stream_zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_REGISTRATION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event_hash: [u8; 32] =
        event.config_sha256.as_slice().try_into().map_err(|_| {
            ExecutorError::ExecutionFailed("MAIL_CONSUMER_HASH_INVALID".to_string())
        })?;
    if super::runtime::configuration::canonical_consumer_sha256(&event) != event_hash {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_CONSUMER_HASH_MISMATCH".to_string(),
        ));
    }
    let config_hash = event
        .config_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let desired_state = if event.desired_state == 2 {
        "ENABLED"
    } else {
        "PAUSED"
    };
    let head_key = consumer_head_key(&consumer_id);
    let snapshot_key = consumer_snapshot_key(&consumer_id, event.config_version);
    let mut snapshot_created = None;

    for _ in 0..4 {
        let current = zone_kv
            .config_entry(head_key.clone())
            .await
            .map_err(|error| ExecutorError::Retryable(format!("ZONE_KV_HEAD_READ: {error}")))?;
        let current_head = current
            .as_ref()
            .map(|entry| serde_json::from_slice::<ConsumerConfigHead>(&entry.value))
            .transpose()
            .map_err(|_| {
                ExecutorError::ExecutionFailed("MAIL_CONSUMER_HEAD_CORRUPT".to_string())
            })?;
        if let Some(head) = &current_head {
            if event.config_version < head.version {
                return Ok(ExecutionResult {
                    message: "mail consumer upsert projection STALE".to_string(),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
            if event.config_version == head.version {
                if head.schema_version != 1
                    || !head.runtime_read_enabled
                    || head.module != "mail"
                    || head.resource_type != "consumer"
                    || head.resource_id != consumer_id
                    || head.config_sha256 != config_hash
                    || head.tombstoned
                    || head.desired_state != desired_state
                    || head.owner_id != owner_id
                    || head.owner_type != event.owner_type
                    || head.workspace_id != workspace_id
                    || head.zone_id != zone_id
                {
                    return Err(ExecutorError::ExecutionFailed(
                        "MAIL_CONSUMER_VERSION_HASH_CONFLICT".to_string(),
                    ));
                }
                let repaired = ensure_immutable_snapshot(&zone_kv, &snapshot_key, &payload.payload)
                    .await
                    .map_err(|error| {
                        ExecutorError::Retryable(format!("ZONE_KV_SNAPSHOT: {error}"))
                    })?;
                return Ok(ExecutionResult {
                    message: format!(
                        "mail consumer upsert projection DUPLICATE{}",
                        if repaired { "/REPAIRED" } else { "" }
                    ),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
        }
        if snapshot_created.is_none() {
            snapshot_created = Some(
                ensure_immutable_snapshot(&zone_kv, &snapshot_key, &payload.payload)
                    .await
                    .map_err(|error| {
                        ExecutorError::Retryable(format!("ZONE_KV_SNAPSHOT: {error}"))
                    })?,
            );
        }
        let head_value = serde_json::to_vec(&ConsumerHeadWrite {
            schema_version: 1,
            runtime_read_enabled: true,
            module: "mail",
            resource_type: "consumer",
            resource_id: &consumer_id,
            version: event.config_version,
            event_id: &event_id,
            config_sha256: &config_hash,
            desired_state,
            tombstoned: false,
            owner_id: &owner_id,
            owner_type: &event.owner_type,
            workspace_id: &workspace_id,
            zone_id: &zone_id,
            // Only a new identity can establish an empty journal baseline.
            // Never certify a legacy consumer by silently upgrading its head.
            runtime_protocol: current_head
                .as_ref()
                .map_or(1, |head| head.runtime_protocol),
            runtime_generations: current_head
                .as_ref()
                .map_or(&[], |head| head.runtime_generations.as_slice()),
        })
        .map_err(|error| ExecutorError::ExecutionFailed(format!("MAIL_HEAD_ENCODE: {error}")))?;
        let result = match current {
            Some(entry) => {
                zone_kv
                    .config_update(&head_key, Bytes::from(head_value), entry.revision)
                    .await
            }
            None => {
                zone_kv
                    .config_create(&head_key, Bytes::from(head_value))
                    .await
            }
        };
        if result.is_ok() {
            return Ok(ExecutionResult {
                message: "mail consumer upsert projection APPLIED".to_string(),
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_consumer_delete(
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event = MailConsumerDeleteV1::decode(payload.payload.as_ref()).map_err(|error| {
        ExecutorError::ExecutionFailed(format!("MAIL_CONSUMER_DELETE_DECODE: {error}"))
    })?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    if payload.payload_schema_version != 1
        || event.consumer_id.len() != 16
        || event.config_version == 0
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_CONSUMER_DELETE_INVALID".to_string(),
        ));
    }
    let consumer_id = uuid::Uuid::from_slice(&event.consumer_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_CONSUMER_ID_INVALID".to_string()))?
        .to_string();
    let head_key = consumer_head_key(&consumer_id);
    for _ in 0..4 {
        let current = zone_kv
            .config_entry(head_key.clone())
            .await
            .map_err(|error| ExecutorError::Retryable(format!("ZONE_KV_HEAD_READ: {error}")))?;
        let current_head = current
            .as_ref()
            .map(|entry| serde_json::from_slice::<ConsumerConfigHead>(&entry.value))
            .transpose()
            .map_err(|_| {
                ExecutorError::ExecutionFailed("MAIL_CONSUMER_HEAD_CORRUPT".to_string())
            })?;
        if let Some(head) = &current_head {
            if event.config_version < head.version {
                return Ok(ExecutionResult {
                    message: "mail consumer delete projection STALE".to_string(),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
            if event.config_version == head.version {
                if !head.tombstoned {
                    return Err(ExecutorError::ExecutionFailed(
                        "MAIL_CONSUMER_DELETE_VERSION_CONFLICT".to_string(),
                    ));
                }
                return Ok(ExecutionResult {
                    message: "mail consumer delete projection DUPLICATE".to_string(),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
        }
        let head = current_head
            .as_ref()
            .ok_or_else(|| ExecutorError::ExecutionFailed("MAIL_DELETE_REQUIRES_DRAINED".into()))?;
        let receipt_key = format!("mail.consumer.drain.result.{consumer_id}.{}", head.version);
        let receipt = zone_kv
            .config_get(&receipt_key)
            .await
            .map_err(ExecutorError::OutcomeUnknown)?
            .ok_or_else(|| ExecutorError::ExecutionFailed("MAIL_DELETE_REQUIRES_DRAINED".into()))?;
        let drained = super::runtime_proto::MailConsumerDrainedV1::decode(receipt)
            .map_err(|_| ExecutorError::ExecutionFailed("MAIL_DRAIN_RECEIPT_CORRUPT".into()))?;
        if head.desired_state != "DRAINING"
            || head.runtime_protocol != 1
            || !head.runtime_generations.is_empty()
            || drained.schema_version != 1
            || drained.consumer_id != event.consumer_id
            || drained.config_version != head.version
        {
            return Err(ExecutorError::ExecutionFailed(
                "MAIL_DELETE_REQUIRES_DRAINED".into(),
            ));
        }
        let value = serde_json::to_vec(&ConsumerHeadWrite {
            schema_version: 1,
            runtime_read_enabled: false,
            module: "mail",
            resource_type: "consumer",
            resource_id: &consumer_id,
            version: event.config_version,
            event_id: &event_id,
            config_sha256: "",
            desired_state: "DELETED",
            tombstoned: true,
            owner_id: current_head
                .as_ref()
                .map_or("", |head| head.owner_id.as_str()),
            owner_type: current_head
                .as_ref()
                .map_or("", |head| head.owner_type.as_str()),
            workspace_id: current_head
                .as_ref()
                .map_or("", |head| head.workspace_id.as_str()),
            zone_id: current_head
                .as_ref()
                .map_or("", |head| head.zone_id.as_str()),
            runtime_protocol: head.runtime_protocol,
            runtime_generations: &head.runtime_generations,
        })
        .map_err(|error| ExecutorError::ExecutionFailed(format!("MAIL_HEAD_ENCODE: {error}")))?;
        let result = match current {
            Some(entry) => {
                zone_kv
                    .config_update(&head_key, Bytes::from(value), entry.revision)
                    .await
            }
            None => zone_kv.config_create(&head_key, Bytes::from(value)).await,
        };
        if result.is_ok() {
            return Ok(ExecutionResult {
                message: "mail consumer delete projection APPLIED".to_string(),
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_consumer_drain(
    job: Arc<ValidatedJob>,
    store: Arc<ZoneKvStore>,
) -> Result<ExecutionResult, ExecutorError> {
    let command = MailConsumerDrainV1::decode(job.payload.as_ref())
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_DRAIN_PROTO_INVALID".into()))?;
    let id = uuid::Uuid::from_slice(&command.consumer_id)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_DRAIN_ID_INVALID".into()))?;
    if command.schema_version != 1
        || job.payload_schema_version != 1
        || id.to_string() != job.resource_id
        || command.config_version == 0
        || command.parallelism == 0
        || command.parallelism > 1024
        || command.timeout_seconds == 0
        || command.timeout_seconds > 3600
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_DRAIN_CONTRACT_INVALID".into(),
        ));
    }
    let head_key = format!("mail.consumer.head.{id}");
    let entry = store
        .config_entry(&head_key)
        .await
        .map_err(ExecutorError::OutcomeUnknown)?
        .ok_or_else(|| ExecutorError::OutcomeUnknown("MAIL_DRAIN_HEAD_MISSING".into()))?;
    let mut head: ConsumerConfigHead = serde_json::from_slice(&entry.value)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_DRAIN_HEAD_CORRUPT".into()))?;
    if head.version != command.config_version
        || head.resource_id != job.resource_id
        || head.zone_id != job.target_zone_id
        || head.tombstoned
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_DRAIN_VERSION_CONFLICT".into(),
        ));
    }
    if head.runtime_protocol != 1 {
        return Err(ExecutorError::OutcomeUnknown(
            "MAIL_DRAIN_LEGACY_RUNTIME_EVIDENCE_REQUIRED".into(),
        ));
    }
    if head.desired_state != "DRAINING" {
        if !matches!(head.desired_state.as_str(), "ENABLED" | "PAUSED") {
            return Err(ExecutorError::ExecutionFailed(
                "MAIL_DRAIN_STATE_CONFLICT".into(),
            ));
        }
        head.desired_state = "DRAINING".into();
        let bytes =
            serde_json::to_vec(&head).map_err(|e| ExecutorError::ExecutionFailed(e.to_string()))?;
        store
            .config_update(&head_key, Bytes::from(bytes), entry.revision)
            .await
            .map_err(ExecutorError::OutcomeUnknown)?;
    }
    let deadline =
        tokio::time::Instant::now() + Duration::from_secs(u64::from(command.timeout_seconds));
    loop {
        // Registration and the drain barrier CAS the same head. An absent
        // lease, an empty replacement slot or a new config version cannot
        // discharge the generations that were admitted before the barrier.
        let head_entry = store
            .config_entry(&head_key)
            .await
            .map_err(ExecutorError::OutcomeUnknown)?
            .ok_or_else(|| ExecutorError::OutcomeUnknown("MAIL_DRAIN_HEAD_MISSING".into()))?;
        let mut observed: ConsumerConfigHead = serde_json::from_slice(&head_entry.value)
            .map_err(|_| ExecutorError::OutcomeUnknown("MAIL_DRAIN_HEAD_CORRUPT".into()))?;
        if observed.version != command.config_version
            || observed.desired_state != "DRAINING"
            || observed.tombstoned
            || observed.runtime_protocol != 1
        {
            return Err(ExecutorError::OutcomeUnknown(
                "MAIL_DRAIN_BARRIER_CHANGED".into(),
            ));
        }
        let mut retired = Vec::new();
        for generation in &observed.runtime_generations {
            let key = format!("mail.consumer.runtime.{id}.{generation}");
            let record = store
                .config_entry(&key)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?
                .ok_or_else(|| {
                    ExecutorError::OutcomeUnknown("MAIL_DRAIN_GENERATION_EVIDENCE_MISSING".into())
                })?;
            let mut journal =
                super::runtime_proto::MailConsumerRuntimeGenerationV1::decode(record.value)
                    .map_err(|_| {
                        ExecutorError::OutcomeUnknown(
                            "MAIL_DRAIN_GENERATION_EVIDENCE_CORRUPT".into(),
                        )
                    })?;
            if journal.schema_version != 1
                || journal.consumer_id != job.resource_id
                || journal.generation_id != *generation
                || journal.config_version == 0
                || journal.config_version > observed.version
                || !(1..=3).contains(&journal.phase)
            {
                return Err(ExecutorError::OutcomeUnknown(
                    "MAIL_DRAIN_GENERATION_SCOPE_CONFLICT".into(),
                ));
            }
            if journal.phase == 1 {
                journal.phase = 3;
                if store
                    .config_update(&key, journal.encode_to_vec().into(), record.revision)
                    .await
                    .is_err()
                {
                    continue;
                }
            }
            if journal.phase == 3 {
                retired.push(generation.clone());
            }
        }
        if !retired.is_empty() {
            observed
                .runtime_generations
                .retain(|generation| !retired.contains(generation));
            let bytes = serde_json::to_vec(&observed)
                .map_err(|e| ExecutorError::OutcomeUnknown(e.to_string()))?;
            if store
                .config_update(&head_key, bytes.into(), head_entry.revision)
                .await
                .is_err()
            {
                continue;
            }
        }
        if observed.runtime_generations.is_empty() {
            let result = MailConsumerDrainedV1 {
                schema_version: 1,
                consumer_id: command.consumer_id.clone(),
                config_version: command.config_version,
                settled_slots: command.parallelism,
            };
            let bytes = result.encode_to_vec();
            let key = format!("mail.consumer.drain.result.{id}.{}", command.config_version);
            if store
                .config_create(&key, Bytes::from(bytes.clone()))
                .await
                .is_err()
            {
                if store
                    .config_get(&key)
                    .await
                    .map_err(ExecutorError::OutcomeUnknown)?
                    .as_deref()
                    != Some(bytes.as_slice())
                {
                    return Err(ExecutorError::OutcomeUnknown(
                        "MAIL_DRAIN_RECEIPT_NOT_DURABLE".into(),
                    ));
                }
            }
            return Ok(ExecutionResult {
                message: "Mail consumer drained".into(),
                result_payload: bytes,
                result_payload_schema_version: 1,
            });
        }
        if tokio::time::Instant::now() >= deadline {
            return Err(ExecutorError::OutcomeUnknown(
                "MAIL_DRAIN_WAITING_FOR_SLOT_SETTLEMENT".into(),
            ));
        }
        tokio::time::sleep(Duration::from_millis(500 + rand::random::<u64>() % 250)).await;
    }
}
