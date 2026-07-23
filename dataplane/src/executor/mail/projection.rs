use super::runtime_proto::{
    KafkaStreamPayloadV1, MailConsumerDeleteV1, MailConsumerUpsertV1,
    MailProjectionReconcileCompletedV1, MailStreamType, MailTemplateDeletedV1,
    MailTemplateVersionPublishedV1, NatsJetStreamPayloadV1, RabbitMqPayloadV1,
    RedisStreamPayloadV1,
};
use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::zone_kv::{ConsumerConfigHead, TemplateConfigHead, ZoneKvStore};
use crate::job_lifecycle::message::JobPayload;
use bytes::Bytes;
use prost::Message;
use serde::Serialize;
use std::sync::Arc;

const CONFIG_CONSUMER_HEAD_PREFIX: &str = "mail.consumer.head.";
const CONFIG_CONSUMER_SNAPSHOT_PREFIX: &str = "mail.consumer.snapshot.";
const CONFIG_TEMPLATE_HEAD_PREFIX: &str = "mail.template.head.";
const CONFIG_TEMPLATE_SNAPSHOT_PREFIX: &str = "mail.template.snapshot.";
const CONFIG_RECONCILED_KEY: &str = "mail.projection.reconciled";

#[derive(Serialize)]
struct ConsumerHeadWrite<'a> {
    version: u64,
    event_id: &'a str,
    config_sha256: &'a str,
    desired_state: &'a str,
    tombstoned: bool,
}

#[derive(Serialize)]
struct TemplateHeadWrite<'a> {
    revision: u64,
    event_id: &'a str,
    current_version: u64,
    content_sha256: &'a str,
    tombstoned: bool,
}

#[derive(serde::Deserialize, Serialize)]
struct ReconcileMarker {
    generation: u64,
    completed_at_unix_ms: i64,
    event_id: String,
}

fn consumer_head_key(id: &str) -> String {
    format!("{CONFIG_CONSUMER_HEAD_PREFIX}{id}")
}

fn consumer_snapshot_key(id: &str, version: u64) -> String {
    format!("{CONFIG_CONSUMER_SNAPSHOT_PREFIX}{id}.v{version}")
}

fn template_head_key(id: &str) -> String {
    format!("{CONFIG_TEMPLATE_HEAD_PREFIX}{id}")
}

fn template_snapshot_key(id: &str, version: u64) -> String {
    format!("{CONFIG_TEMPLATE_SNAPSHOT_PREFIX}{id}.v{version}")
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
    payload: &JobPayload,
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
    payload: JobPayload,
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
    let event = MailConsumerUpsertV1::decode(payload.payload.as_slice()).map_err(|error| {
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
                });
            }
            if event.config_version == head.version {
                if head.config_sha256 != config_hash
                    || head.tombstoned
                    || head.desired_state != desired_state
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
            version: event.config_version,
            event_id: &event_id,
            config_sha256: &config_hash,
            desired_state,
            tombstoned: false,
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
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_consumer_delete(
    payload: JobPayload,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event = MailConsumerDeleteV1::decode(payload.payload.as_slice()).map_err(|error| {
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
                });
            }
        }
        let value = serde_json::to_vec(&ConsumerHeadWrite {
            version: event.config_version,
            event_id: &event_id,
            config_sha256: "",
            desired_state: "DELETED",
            tombstoned: true,
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
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_template_version_published(
    payload: JobPayload,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event =
        MailTemplateVersionPublishedV1::decode(payload.payload.as_slice()).map_err(|error| {
            ExecutorError::ExecutionFailed(format!("MAIL_TEMPLATE_PUBLISH_DECODE: {error}"))
        })?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    // [COMMENT]: Fail-close khi zstd decode lỗi, vượt giới hạn size (streaming take), hoặc HTML không phải UTF-8 hợp lệ; không fallback raw text.
    use std::io::Read;
    let decoder = zstd::Decoder::new(event.html_template.as_slice())
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_TEMPLATE_ZSTD_DECODE_FAILED".to_string()))?;
    let mut limited = decoder.take((3 << 20) + 1);
    let mut decompressed_html = Vec::new();
    limited.read_to_end(&mut decompressed_html)
        .map_err(|_| ExecutorError::ExecutionFailed("MAIL_TEMPLATE_ZSTD_DECODE_FAILED".to_string()))?;
    if decompressed_html.len() > 3 << 20 {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_TEMPLATE_DECOMPRESSED_SIZE_EXCEEDED".to_string(),
        ));
    }
    if std::str::from_utf8(&decompressed_html).is_err() {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_TEMPLATE_UTF8_INVALID".to_string(),
        ));
    }
    if payload.payload_schema_version != 1
        || event.template_id.trim().is_empty()
        || uuid::Uuid::parse_str(&event.template_id).is_err()
        || event.template_revision == 0
        || event.template_version == 0
        || event.subject_template.trim().is_empty()
        || event.html_template.is_empty()
        || event.content_sha256.len() != 32
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_TEMPLATE_PUBLISH_INVALID".to_string(),
        ));
    }
    let content_hash = event
        .content_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let event_hash: [u8; 32] =
        event.content_sha256.as_slice().try_into().map_err(|_| {
            ExecutorError::ExecutionFailed("MAIL_TEMPLATE_HASH_INVALID".to_string())
        })?;
    if super::runtime::configuration::canonical_template_sha256(
        &event.subject_template,
        &decompressed_html,
    ) != event_hash
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_TEMPLATE_HASH_MISMATCH".to_string(),
        ));
    }
    let head_key = template_head_key(&event.template_id);
    let snapshot_key = template_snapshot_key(&event.template_id, event.template_version);
    let mut snapshot_created = None;
    for _ in 0..4 {
        let current = zone_kv
            .config_entry(head_key.clone())
            .await
            .map_err(|error| ExecutorError::Retryable(format!("ZONE_KV_HEAD_READ: {error}")))?;
        let current_head = current
            .as_ref()
            .map(|entry| serde_json::from_slice::<TemplateConfigHead>(&entry.value))
            .transpose()
            .map_err(|_| {
                ExecutorError::ExecutionFailed("MAIL_TEMPLATE_HEAD_CORRUPT".to_string())
            })?;
        if let Some(head) = &current_head {
            if event.template_revision < head.revision {
                return Ok(ExecutionResult {
                    message: "mail template projection STALE".to_string(),
                });
            }
            if event.template_revision == head.revision {
                if head.content_sha256 != content_hash
                    || head.current_version != event.template_version
                    || head.tombstoned
                {
                    return Err(ExecutorError::ExecutionFailed(
                        "MAIL_TEMPLATE_VERSION_HASH_CONFLICT".to_string(),
                    ));
                }
                let repaired = ensure_immutable_snapshot(&zone_kv, &snapshot_key, &payload.payload)
                    .await
                    .map_err(|error| {
                        ExecutorError::Retryable(format!("ZONE_KV_SNAPSHOT: {error}"))
                    })?;
                return Ok(ExecutionResult {
                    message: format!(
                        "mail template projection DUPLICATE{}",
                        if repaired { "/REPAIRED" } else { "" }
                    ),
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
        let value = serde_json::to_vec(&TemplateHeadWrite {
            revision: event.template_revision,
            event_id: &event_id,
            current_version: event.template_version,
            content_sha256: &content_hash,
            tombstoned: false,
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
                message: "mail template projection APPLIED".to_string(),
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_template_deleted(
    payload: JobPayload,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event = MailTemplateDeletedV1::decode(payload.payload.as_slice()).map_err(|error| {
        ExecutorError::ExecutionFailed(format!("MAIL_TEMPLATE_DELETE_DECODE: {error}"))
    })?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    if payload.payload_schema_version != 1
        || event.template_id.trim().is_empty()
        || uuid::Uuid::parse_str(&event.template_id).is_err()
        || event.template_revision == 0
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_TEMPLATE_DELETE_INVALID".to_string(),
        ));
    }
    let head_key = template_head_key(&event.template_id);
    for _ in 0..4 {
        let current = zone_kv
            .config_entry(head_key.clone())
            .await
            .map_err(|error| ExecutorError::Retryable(format!("ZONE_KV_HEAD_READ: {error}")))?;
        let current_head = current
            .as_ref()
            .map(|entry| serde_json::from_slice::<TemplateConfigHead>(&entry.value))
            .transpose()
            .map_err(|_| {
                ExecutorError::ExecutionFailed("MAIL_TEMPLATE_HEAD_CORRUPT".to_string())
            })?;
        if let Some(head) = &current_head {
            if event.template_revision < head.revision {
                return Ok(ExecutionResult {
                    message: "mail template delete projection STALE".to_string(),
                });
            }
            if event.template_revision == head.revision {
                if !head.tombstoned {
                    return Err(ExecutorError::ExecutionFailed(
                        "MAIL_TEMPLATE_DELETE_VERSION_CONFLICT".to_string(),
                    ));
                }
                return Ok(ExecutionResult {
                    message: "mail template delete projection DUPLICATE".to_string(),
                });
            }
        }
        let value = serde_json::to_vec(&TemplateHeadWrite {
            revision: event.template_revision,
            event_id: &event_id,
            current_version: event.last_published_version,
            content_sha256: "",
            tombstoned: true,
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
                message: "mail template delete projection APPLIED".to_string(),
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_reconcile_completed(
    payload: JobPayload,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event = MailProjectionReconcileCompletedV1::decode(payload.payload.as_slice()).map_err(
        |error| ExecutorError::ExecutionFailed(format!("MAIL_RECONCILE_MARKER_DECODE: {error}")),
    )?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    if payload.payload_schema_version != 1
        || event.reconcile_generation == 0
        || event.completed_at_unix_ms <= 0
    {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_RECONCILE_MARKER_INVALID".to_string(),
        ));
    }
    for _ in 0..4 {
        let current = zone_kv
            .config_entry(CONFIG_RECONCILED_KEY.to_string())
            .await
            .map_err(|error| {
                ExecutorError::Retryable(format!("ZONE_KV_RECONCILE_READ: {error}"))
            })?;
        let current_marker = current
            .as_ref()
            .map(|entry| serde_json::from_slice::<ReconcileMarker>(&entry.value))
            .transpose()
            .map_err(|_| {
                ExecutorError::ExecutionFailed("MAIL_RECONCILE_MARKER_CORRUPT".to_string())
            })?;
        if current_marker
            .as_ref()
            .is_some_and(|marker| marker.generation >= event.reconcile_generation)
        {
            return Ok(ExecutionResult {
                message: "mail reconcile marker STALE_OR_DUPLICATE".to_string(),
            });
        }
        let marker = ReconcileMarker {
            generation: event.reconcile_generation,
            completed_at_unix_ms: event.completed_at_unix_ms,
            event_id: event_id.clone(),
        };
        let value = Bytes::from(serde_json::to_vec(&marker).map_err(|error| {
            ExecutorError::ExecutionFailed(format!("MAIL_RECONCILE_MARKER_ENCODE: {error}"))
        })?);
        let result = match current {
            Some(entry) => {
                zone_kv
                    .config_update(CONFIG_RECONCILED_KEY, value, entry.revision)
                    .await
            }
            None => zone_kv.config_create(CONFIG_RECONCILED_KEY, value).await,
        };
        if result.is_ok() {
            return Ok(ExecutionResult {
                message: "mail reconcile marker APPLIED".to_string(),
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_RECONCILE_CAS_CONTENTION".to_string(),
    ))
}
