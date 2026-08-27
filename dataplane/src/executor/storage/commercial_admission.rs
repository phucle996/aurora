use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use async_nats::jetstream::kv;
use async_trait::async_trait;
use bytes::Bytes;
use prost::Message;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

pub mod proto {
    include!(concat!(env!("OUT_DIR"), "/controlplane.storage.v1.rs"));
}

/// Record cấu trúc quyết định Commercial Admission lưu trữ tại NATS JetStream KV (`AURORA_ZONE_ADMISSION`).
#[derive(Clone, Debug, Deserialize, Serialize, Eq, PartialEq)]
pub struct AdmissionRecord {
    #[serde(default)]
    pub resource_id: String,
    #[serde(default)]
    pub resource_name: String,
    pub policy_version: i64,
    pub decision: String,
    pub effective_at_unix_seconds: i64,
    pub valid_until_unix_seconds: Option<i64>,
}

/// [COMMENT]: Bộ thực thi cập nhật quyền thương mại của Bucket (Commercial Admission) tại Zone.
///
/// Bối cảnh:
/// - Khi nhận sự kiện thay đổi quyền thương mại từ Central (`storage.bucket.commercial_admission`),
///   Executor này giải mã protobuf `StorageAdmissionChangedV1` và cập nhật trực tiếp vào `AURORA_ZONE_ADMISSION`
///   trong NATS JetStream KV store của Zone.
/// - Zone Public Authorizer tại Edge Gateway đọc trực tiếp bản ghi này để cho phép (`ALLOW`)
///   hoặc chặn đứng (`SUSPEND_BILLABLE`) các request truy cập I/O của bucket tại Edge trong 0ms.
pub struct BucketCommercialAdmissionExecutor {
    zone_kv: Arc<ZoneKvStore>,
}

impl BucketCommercialAdmissionExecutor {
    pub fn new(zone_kv: Arc<ZoneKvStore>) -> Self {
        Self { zone_kv }
    }
}

#[async_trait]
impl Executor for BucketCommercialAdmissionExecutor {
    async fn execute(&self, job: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let request =
            proto::StorageAdmissionChangedV1::decode(&job.payload[..]).map_err(|error| {
                ExecutorError::ExecutionFailed(format!(
                    "decode storage commercial admission command failed: {error}"
                ))
            })?;

        validate(&request, &job)?;

        Logger::sys_info(
            "executor.storage.commercial_admission",
            &format!(
				"Applying commercial admission: bucket_id={}, bucket_name={}, decision={}, policy_version={}",
				request.resource_id, request.resource_name, request.decision, request.policy_version
			),
        );

        let effective_at = chrono::DateTime::parse_from_rfc3339(&request.effective_at)
            .map(|dt| dt.timestamp())
            .unwrap_or_else(|_| chrono::Utc::now().timestamp());

        let valid_until = if !request.valid_until.is_empty() {
            chrono::DateTime::parse_from_rfc3339(&request.valid_until)
                .map(|dt| dt.timestamp())
                .ok()
        } else {
            None
        };

        let record = AdmissionRecord {
            resource_id: request.resource_id,
            resource_name: request.resource_name,
            policy_version: request.policy_version,
            decision: request.decision,
            effective_at_unix_seconds: effective_at,
            valid_until_unix_seconds: valid_until,
        };

        apply_admission_to_kv(self.zone_kv.admission(), &record).await?;

        Ok(ExecutionResult {
            message: "ADMISSION_SYNCED".to_string(),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        })
    }
}

/// Idempotent monotonic admission projection in Zone KV.
/// Replays with identical or older policy versions are no-ops;
/// higher policy versions update both name and resource_id keys.
async fn apply_admission_to_kv(
    store: &kv::Store,
    record: &AdmissionRecord,
) -> Result<(), ExecutorError> {
    let value = Bytes::from(serde_json::to_vec(record).map_err(|error| {
        ExecutorError::ExecutionFailed(format!("serialize admission record failed: {error}"))
    })?);
    let name_key = format!("name/{}", record.resource_name);

    // Monotonic CAS loop for name/key
    for _ in 0..5 {
        let current = store.entry(name_key.clone()).await.map_err(|error| {
            ExecutorError::ExecutionFailed(format!("read admission by name failed: {error}"))
        })?;
        if let Some(entry) = &current {
            let existing: AdmissionRecord =
                serde_json::from_slice(&entry.value).map_err(|error| {
                    ExecutorError::ExecutionFailed(format!("admission record corrupt: {error}"))
                })?;
            if existing.policy_version >= record.policy_version {
                break;
            }
            if store
                .update(&name_key, value.clone(), entry.revision)
                .await
                .is_ok()
            {
                break;
            }
        } else if store.update(&name_key, value.clone(), 0).await.is_ok() {
            break;
        }
    }

    // Monotonic CAS loop for resource_id key
    for _ in 0..5 {
        let current = store
            .entry(record.resource_id.clone())
            .await
            .map_err(|error| {
                ExecutorError::ExecutionFailed(format!("read admission by id failed: {error}"))
            })?;
        if let Some(entry) = &current {
            let existing: AdmissionRecord =
                serde_json::from_slice(&entry.value).map_err(|error| {
                    ExecutorError::ExecutionFailed(format!("admission record corrupt: {error}"))
                })?;
            if existing.policy_version >= record.policy_version {
                return Ok(());
            }
            if store
                .update(&record.resource_id, value.clone(), entry.revision)
                .await
                .is_ok()
            {
                return Ok(());
            }
        } else if store
            .update(&record.resource_id, value.clone(), 0)
            .await
            .is_ok()
        {
            return Ok(());
        }
    }

    Ok(())
}

fn validate(
    request: &proto::StorageAdmissionChangedV1,
    job: &ValidatedJob,
) -> Result<(), ExecutorError> {
    if request.zone_id != job.target_zone_id {
        return Err(ExecutorError::ExecutionFailed(format!(
            "storage commercial admission zone mismatch: request={}, job={}",
            request.zone_id, job.target_zone_id
        )));
    }
    if request.resource_id != job.resource_id {
        return Err(ExecutorError::ExecutionFailed(format!(
            "storage commercial admission resource mismatch: request={}, job={}",
            request.resource_id, job.resource_id
        )));
    }
    if request.resource_id.is_empty() || request.resource_name.is_empty() {
        return Err(ExecutorError::ExecutionFailed(
            "storage commercial admission bucket id or name is empty".to_string(),
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_zone_and_resource_confusion() {
        let zone_id = uuid::Uuid::new_v4().to_string();
        let resource_id = uuid::Uuid::new_v4().to_string();

        let request = proto::StorageAdmissionChangedV1 {
            event_id: uuid::Uuid::new_v4().to_string(),
            owner_id: uuid::Uuid::new_v4().to_string(),
            owner_type: "PERSONAL".to_string(),
            policy_version: 1,
            decision: "ALLOW".to_string(),
            restriction_reason: String::new(),
            effective_at: chrono::Utc::now().to_rfc3339(),
            valid_until: String::new(),
            resource_id: resource_id.clone(),
            resource_name: "test-bucket".to_string(),
            zone_id: zone_id.clone(),
        };

        let mut command = crate::infra::kafka::transport_proto::JobCommandV1 {
			job_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
			job_version: 1,
			job_topic: "storage.bucket.commercial_admission".to_string(),
			source_domain: "STORAGE".to_string(),
			resource_id: resource_id.clone(),
			payload_schema_version: 1,
			target_zone_id: uuid::Uuid::new_v4().to_string(), // mismatched zone
			transport_schema_version: 1,
			payload_encoding: crate::infra::kafka::transport_proto::PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
			..Default::default()
		};

        let keyring = crate::security::jobpayload::PayloadKeyring::for_test();
        command.payload = keyring.protect_for_test(
            uuid::Uuid::parse_str(&command.target_zone_id).unwrap(),
            &command.source_domain,
            &command.job_topic,
            &command.resource_id,
            command.job_version,
            command.payload_schema_version,
            &[1],
        );

        let job = ValidatedJob::decode(
            &command.encode_to_vec(),
            &command.target_zone_id,
            3,
            &keyring,
        )
        .unwrap();

        // Zone mismatch -> should fail validation
        assert!(validate(&request, &job).is_err());
    }
}
