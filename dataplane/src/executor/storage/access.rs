use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::zone_kv::{StorageAccessRecord, ZoneKvStore};
use crate::job_runtime::model::ValidatedJob;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use super::storage_proto;

pub struct StorageAccessPrepareExecutor {
    zone_kv: Arc<ZoneKvStore>,
}

impl StorageAccessPrepareExecutor {
    pub fn new(zone_kv: Arc<ZoneKvStore>) -> Self {
        Self { zone_kv }
    }
}

#[async_trait]
impl Executor for StorageAccessPrepareExecutor {
    async fn execute(&self, job: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let request = storage_proto::StorageAccessPrepareRequest::decode(&job.payload[..])
            .map_err(|error| {
                ExecutorError::ExecutionFailed(format!(
                    "decode storage access command failed: {error}"
                ))
            })?;
        validate(&request, &job)?;

        let record = StorageAccessRecord {
            access_session_id: request.access_session_id,
            binding_hash: request.binding_hash,
            actor_id: request.actor_id,
            resource_id: request.resource_id,
            bucket_name: request.bucket_name,
            workspace_id: request.workspace_id,
            zone_id: request.zone_id,
            actions: request.actions,
            key_prefix: request.key_prefix,
            expires_at_unix_seconds: request.expires_at_unix_seconds,
            policy_revision: request.policy_revision,
        };
        self.zone_kv
            .access_put(&record)
            .await
            .map_err(ExecutorError::ExecutionFailed)?;

        let _ready_contract = storage_proto::StorageAccessPrepareResponse {
            access_session_id: record.access_session_id.clone(),
            resource_id: record.resource_id.clone(),
            zone_id: record.zone_id.clone(),
            expires_at_unix_seconds: record.expires_at_unix_seconds,
            state: "ACTIVE".to_string(),
        };

        // Result payload intentionally carries no binding hash or credentials.
        // The opaque id was already returned by Controlplane to the client.
        Ok(ExecutionResult {
            message: "ACCESS_READY".to_string(),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        })
    }
}

fn validate(
    request: &storage_proto::StorageAccessPrepareRequest,
    job: &ValidatedJob,
) -> Result<(), ExecutorError> {
    for (name, value) in [
        ("access_session_id", request.access_session_id.as_str()),
        ("actor_id", request.actor_id.as_str()),
        ("resource_id", request.resource_id.as_str()),
        ("workspace_id", request.workspace_id.as_str()),
        ("zone_id", request.zone_id.as_str()),
    ] {
        uuid::Uuid::parse_str(value).map_err(|_| {
            ExecutorError::ExecutionFailed(format!("{name} must be a non-nil UUID"))
        })?;
    }
    if request.zone_id != job.target_zone_id {
        return Err(ExecutorError::ExecutionFailed(
            "storage access command Zone binding mismatch".to_string(),
        ));
    }
    if request.resource_id != job.resource_id {
        return Err(ExecutorError::ExecutionFailed(
            "storage access command resource fence mismatch".to_string(),
        ));
    }
    if request.bucket_name.is_empty() || request.bucket_name.len() > 255 {
        return Err(ExecutorError::ExecutionFailed(
            "storage access bucket_name is invalid".to_string(),
        ));
    }
    if request.binding_hash.len() != 64
        || !request
            .binding_hash
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit())
    {
        return Err(ExecutorError::ExecutionFailed(
            "storage access binding_hash must be SHA-256 hex".to_string(),
        ));
    }
    if request.key_prefix.len() > 256 || request.key_prefix.contains(['\r', '\n']) {
        return Err(ExecutorError::ExecutionFailed(
            "storage access key_prefix is invalid".to_string(),
        ));
    }
    if request.actions.is_empty() || request.actions.len() > 16 {
        return Err(ExecutorError::ExecutionFailed(
            "storage access actions are missing or oversized".to_string(),
        ));
    }
    const ALLOWED: [&str; 6] = [
        "ListBucket",
        "GetObject",
        "PutObject",
        "DeleteObject",
        "GetObjectTagging",
        "PutObjectTagging",
    ];
    if request
        .actions
        .iter()
        .any(|action| !ALLOWED.contains(&action.as_str()))
    {
        return Err(ExecutorError::ExecutionFailed(
            "storage access action is not allowed".to_string(),
        ));
    }
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| {
            ExecutorError::ExecutionFailed("system clock is before Unix epoch".to_string())
        })?
        .as_secs();
    if request.expires_at_unix_seconds <= now
        || request.expires_at_unix_seconds > now.saturating_add(3_660)
        || request.policy_revision == 0
    {
        return Err(ExecutorError::ExecutionFailed(
            "storage access expiry or policy revision is invalid".to_string(),
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_zone_confusion() {
        let zone = uuid::Uuid::new_v4().to_string();
        let request = storage_proto::StorageAccessPrepareRequest {
            access_session_id: uuid::Uuid::new_v4().to_string(),
            binding_hash: "a".repeat(64),
            actor_id: uuid::Uuid::new_v4().to_string(),
            resource_id: uuid::Uuid::new_v4().to_string(),
            bucket_name: "bucket".to_string(),
            workspace_id: uuid::Uuid::new_v4().to_string(),
            zone_id: zone,
            actions: vec!["ListBucket".to_string()],
            key_prefix: String::new(),
            expires_at_unix_seconds: u64::MAX,
            policy_revision: 1,
        };
        let mut command = crate::infra::kafka::transport_proto::JobCommandV1::default();
        command.job_id = uuid::Uuid::new_v4().as_bytes().to_vec();
        command.job_version = 1;
        command.job_topic = "storage.access.prepare".to_string();
        command.source_domain = "STORAGE".to_string();
        command.resource_id = request.resource_id.clone();
        command.payload_schema_version = 1;
        command.target_zone_id = uuid::Uuid::new_v4().to_string();
        command.transport_schema_version = 1;
        let job =
            ValidatedJob::decode(&command.encode_to_vec(), &command.target_zone_id, 3).unwrap();
        assert!(validate(&request, &job).is_err());
    }
}
