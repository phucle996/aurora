use super::runtime_proto::{
    MailProjectionReconcileCompletedV1, MailTemplateDeletedV1, MailTemplateVersionPublishedV1,
};
use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::zone_kv::{TemplateConfigHead, ZoneKvStore};
use crate::job_runtime::model::ValidatedJob;
use bytes::Bytes;
use prost::Message;
use serde::Serialize;
use std::sync::Arc;

const CONFIG_TEMPLATE_HEAD_PREFIX: &str = "mail.template.head.";
const CONFIG_TEMPLATE_SNAPSHOT_PREFIX: &str = "mail.template.snapshot.";
const CONFIG_RECONCILED_KEY: &str = "mail.projection.reconciled";

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

pub async fn apply_mail_template_version_published(
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event =
        MailTemplateVersionPublishedV1::decode(payload.payload.as_ref()).map_err(|error| {
            ExecutorError::ExecutionFailed(format!("MAIL_TEMPLATE_PUBLISH_DECODE: {error}"))
        })?;
    let metadata = event.metadata.as_ref().ok_or_else(|| {
        ExecutorError::ExecutionFailed("MAIL_EVENT_METADATA_REQUIRED".to_string())
    })?;
    let event_id = event_id(&payload, metadata)?;
    // [COMMENT]: Fail-close khi zstd decode lỗi, vượt giới hạn size (streaming take), hoặc HTML không phải UTF-8 hợp lệ; không fallback raw text.
    use std::io::Read;
    let decoder = zstd::Decoder::new(event.html_template.as_slice()).map_err(|_| {
        ExecutorError::ExecutionFailed("MAIL_TEMPLATE_ZSTD_DECODE_FAILED".to_string())
    })?;
    let mut limited = decoder.take((3 << 20) + 1);
    let mut decompressed_html = Vec::new();
    limited.read_to_end(&mut decompressed_html).map_err(|_| {
        ExecutorError::ExecutionFailed("MAIL_TEMPLATE_ZSTD_DECODE_FAILED".to_string())
    })?;
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
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
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
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_template_deleted(
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event = MailTemplateDeletedV1::decode(payload.payload.as_ref()).map_err(|error| {
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
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
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
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
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
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_HEAD_CAS_CONTENTION".to_string(),
    ))
}

pub async fn apply_mail_reconcile_completed(
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
    stream_zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    if stream_zone_id != crate::config::Config::get_global().zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "MAIL_PROJECTION_ZONE_MISMATCH".to_string(),
        ));
    }
    let event =
        MailProjectionReconcileCompletedV1::decode(payload.payload.as_ref()).map_err(|error| {
            ExecutorError::ExecutionFailed(format!("MAIL_RECONCILE_MARKER_DECODE: {error}"))
        })?;
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
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
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
                result_payload: Vec::new(),
                result_payload_schema_version: 0,
            });
        }
    }
    Err(ExecutorError::Retryable(
        "ZONE_KV_RECONCILE_CAS_CONTENTION".to_string(),
    ))
}
