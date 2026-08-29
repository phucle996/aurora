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
    #[serde(default)]
    pub restriction_reason: Option<String>,
    pub effective_at_unix_seconds: i64,
    pub valid_until_unix_seconds: Option<i64>,
    #[serde(default)]
    pub source_event_id: String,
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

#[derive(Deserialize)]
struct AdmissionRuntimeHead {
    schema_version: u32,
    runtime_read_enabled: bool,
    module: String,
    resource_type: String,
    resource_id: String,
    resource_name: String,
    tombstoned: bool,
    owner_id: String,
    owner_type: String,
    zone_id: String,
}

enum AdmissionTargetState {
    Active,
    Inactive,
    Absent,
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
            .map_err(|_| {
                ExecutorError::ExecutionFailed(
                    "storage commercial admission effective_at is invalid".to_string(),
                )
            })?;

        let valid_until = if !request.valid_until.is_empty() {
            let valid_until = chrono::DateTime::parse_from_rfc3339(&request.valid_until)
                .map(|dt| dt.timestamp())
                .map_err(|_| {
                    ExecutorError::ExecutionFailed(
                        "storage commercial admission valid_until is invalid".to_string(),
                    )
                })?;
            if valid_until <= effective_at {
                return Err(ExecutorError::ExecutionFailed(
                    "storage commercial admission validity window is invalid".to_string(),
                ));
            }
            Some(valid_until)
        } else {
            None
        };

        let record = AdmissionRecord {
            resource_id: request.resource_id.clone(),
            resource_name: request.resource_name.clone(),
            policy_version: request.policy_version,
            decision: request.decision.clone(),
            restriction_reason: if request.restriction_reason.is_empty() {
                None
            } else {
                Some(request.restriction_reason.clone())
            },
            effective_at_unix_seconds: effective_at,
            valid_until_unix_seconds: valid_until,
            source_event_id: request.event_id.clone(),
        };

        match admission_target_state(&self.zone_kv, &request).await? {
            AdmissionTargetState::Active => {}
            AdmissionTargetState::Inactive => {
                return Ok(ExecutionResult {
                    message: "ADMISSION_TARGET_INACTIVE".to_string(),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
            AdmissionTargetState::Absent => {
                return Err(ExecutorError::Retryable(
                    "STORAGE_ADMISSION_RUNTIME_HEAD_PENDING".to_string(),
                ));
            }
        }

        apply_admission_to_kv(self.zone_kv.admission(), &record).await?;

        match admission_target_state(&self.zone_kv, &request).await? {
            AdmissionTargetState::Active => {}
            AdmissionTargetState::Inactive => {
                remove_admission_from_kv(
                    self.zone_kv.admission(),
                    &record.resource_id,
                    &record.resource_name,
                )
                .await?;
                return Ok(ExecutionResult {
                    message: "ADMISSION_TARGET_BECAME_INACTIVE".to_string(),
                    result_payload: Vec::new(),
                    result_payload_schema_version: 0,
                });
            }
            AdmissionTargetState::Absent => {
                remove_admission_from_kv(
                    self.zone_kv.admission(),
                    &record.resource_id,
                    &record.resource_name,
                )
                .await?;
                return Err(ExecutorError::Retryable(
                    "STORAGE_ADMISSION_RUNTIME_HEAD_DISAPPEARED".to_string(),
                ));
            }
        }

        Ok(ExecutionResult {
            message: "ADMISSION_SYNCED".to_string(),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        })
    }
}

async fn admission_target_state(
    zone_kv: &ZoneKvStore,
    request: &proto::StorageAdmissionChangedV1,
) -> Result<AdmissionTargetState, ExecutorError> {
    let key = format!("storage.bucket.head.{}", request.resource_id);
    let Some(entry) = zone_kv
        .config_entry(&key)
        .await
        .map_err(ExecutorError::OutcomeUnknown)?
    else {
        return Ok(AdmissionTargetState::Absent);
    };
    let head: AdmissionRuntimeHead = serde_json::from_slice(&entry.value).map_err(|error| {
        ExecutorError::ExecutionFailed(format!(
            "storage admission runtime head is corrupt: {error}"
        ))
    })?;
    if head.schema_version != 1
        || head.module != "storage"
        || head.resource_type != "bucket"
        || head.resource_id != request.resource_id
        || head.resource_name != request.resource_name
        || head.owner_id != request.owner_id
        || head.owner_type != request.owner_type
        || head.zone_id != request.zone_id
    {
        return Err(ExecutorError::ExecutionFailed(
            "STORAGE_ADMISSION_RUNTIME_HEAD_CONFLICT".to_string(),
        ));
    }
    if head.tombstoned || !head.runtime_read_enabled {
        return Ok(AdmissionTargetState::Inactive);
    }
    Ok(AdmissionTargetState::Active)
}

/// Idempotent monotonic admission projection in Zone KV.
/// Replays with identical or older policy versions are no-ops;
/// higher policy versions update both name and resource_id keys.
pub(super) async fn apply_admission_to_kv(
    store: &kv::Store,
    record: &AdmissionRecord,
) -> Result<(), ExecutorError> {
    let name_key = format!("name/{}", record.resource_name);
    for _ in 0..5 {
        let name_entry = store.entry(name_key.clone()).await.map_err(|error| {
            ExecutorError::OutcomeUnknown(format!("read admission by name failed: {error}"))
        })?;
        let id_entry = store
            .entry(record.resource_id.clone())
            .await
            .map_err(|error| {
                ExecutorError::OutcomeUnknown(format!("read admission by id failed: {error}"))
            })?;

        let mut desired = record.clone();
        let mut name_existing = None;
        let mut id_existing = None;
        for (entry, decoded, conflict) in [
            (
                name_entry.as_ref(),
                &mut name_existing,
                "STORAGE_ADMISSION_NAME_RESOURCE_CONFLICT",
            ),
            (
                id_entry.as_ref(),
                &mut id_existing,
                "STORAGE_ADMISSION_RESOURCE_IDENTITY_CONFLICT",
            ),
        ] {
            let Some(entry) = entry else { continue };
            let existing: AdmissionRecord =
                serde_json::from_slice(&entry.value).map_err(|error| {
                    ExecutorError::ExecutionFailed(format!("admission record corrupt: {error}"))
                })?;
            if existing.resource_id != record.resource_id
                || existing.resource_name != record.resource_name
            {
                return Err(ExecutorError::ExecutionFailed(conflict.to_string()));
            }
            if existing.policy_version > desired.policy_version {
                desired = existing.clone();
            } else if existing.policy_version == desired.policy_version && existing != desired {
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_ADMISSION_EQUAL_VERSION_CONFLICT".to_string(),
                ));
            }
            *decoded = Some(existing);
        }

        // Re-check both decoded values after choosing the maximum winner. A
        // same-version disagreement across indexes is corruption, not replay.
        for existing in [&name_existing, &id_existing].into_iter().flatten() {
            if existing.policy_version == desired.policy_version && *existing != desired {
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_ADMISSION_EQUAL_VERSION_CONFLICT".to_string(),
                ));
            }
        }

        let value = Bytes::from(serde_json::to_vec(&desired).map_err(|error| {
            ExecutorError::ExecutionFailed(format!("serialize admission record failed: {error}"))
        })?);
        let name_settled = match (&name_entry, &name_existing) {
            (Some(_), Some(existing)) if *existing == desired => true,
            (Some(entry), Some(existing)) if existing.policy_version < desired.policy_version => {
                store
                    .update(&name_key, value.clone(), entry.revision)
                    .await
                    .is_ok()
            }
            (None, None) => store.update(&name_key, value.clone(), 0).await.is_ok(),
            _ => false,
        };
        if !name_settled {
            continue;
        }

        let id_settled = match (&id_entry, &id_existing) {
            (Some(_), Some(existing)) if *existing == desired => true,
            (Some(entry), Some(existing)) if existing.policy_version < desired.policy_version => {
                store
                    .update(&record.resource_id, value.clone(), entry.revision)
                    .await
                    .is_ok()
            }
            (None, None) => store
                .update(&record.resource_id, value.clone(), 0)
                .await
                .is_ok(),
            _ => false,
        };
        if !id_settled {
            continue;
        }

        let settled_name = store.entry(name_key.clone()).await.map_err(|error| {
            ExecutorError::OutcomeUnknown(format!("verify admission by name failed: {error}"))
        })?;
        let settled_id = store
            .entry(record.resource_id.clone())
            .await
            .map_err(|error| {
                ExecutorError::OutcomeUnknown(format!("verify admission by id failed: {error}"))
            })?;
        if [&settled_name, &settled_id].iter().all(|entry| {
            entry.as_ref().is_some_and(|entry| {
                serde_json::from_slice::<AdmissionRecord>(&entry.value)
                    .is_ok_and(|stored| stored == desired)
            })
        }) {
            return Ok(());
        }
    }

    Err(ExecutorError::Retryable(
        "STORAGE_ADMISSION_INDEX_CAS_CONTENTION".to_string(),
    ))
}

pub(super) async fn remove_admission_from_kv(
    store: &kv::Store,
    resource_id: &str,
    resource_name: &str,
) -> Result<(), ExecutorError> {
    for key in [format!("name/{resource_name}"), resource_id.to_string()] {
        let mut removed = false;
        for _ in 0..5 {
            let current = store.entry(key.clone()).await.map_err(|error| {
                ExecutorError::OutcomeUnknown(format!(
                    "read Storage admission cleanup key failed: {error}"
                ))
            })?;
            let Some(entry) = current else {
                removed = true;
                break;
            };
            let existing: AdmissionRecord =
                serde_json::from_slice(&entry.value).map_err(|error| {
                    ExecutorError::ExecutionFailed(format!(
                        "Storage admission cleanup record corrupt: {error}"
                    ))
                })?;
            if existing.resource_id != resource_id || existing.resource_name != resource_name {
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_ADMISSION_CLEANUP_IDENTITY_CONFLICT".to_string(),
                ));
            }
            if store
                .delete_expect_revision(&key, Some(entry.revision))
                .await
                .is_ok()
            {
                removed = true;
                break;
            }
        }
        if !removed {
            return Err(ExecutorError::Retryable(
                "STORAGE_ADMISSION_CLEANUP_CAS_CONTENTION".to_string(),
            ));
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
    let expected_prefix = match request.owner_type.as_str() {
        "PERSONAL" => "ws-",
        "TENANT" => "tn-",
        _ => "",
    };
    if uuid::Uuid::parse_str(&request.resource_id).is_err()
        || uuid::Uuid::parse_str(&request.zone_id).is_err()
        || request.resource_name.is_empty()
        || request.resource_name.len() > 63
        || !request.resource_name.starts_with(expected_prefix)
        || !request
            .resource_name
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        || uuid::Uuid::parse_str(&request.event_id).is_err()
        || uuid::Uuid::parse_str(&request.owner_id).is_err()
        || !matches!(request.owner_type.as_str(), "PERSONAL" | "TENANT")
        || request.policy_version <= 0
        || !matches!(request.decision.as_str(), "ALLOW" | "SUSPEND_BILLABLE")
        || (request.decision == "ALLOW" && !request.restriction_reason.is_empty())
        || (request.decision == "SUSPEND_BILLABLE" && request.restriction_reason.is_empty())
    {
        return Err(ExecutorError::ExecutionFailed(
            "storage commercial admission contract is invalid".to_string(),
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn allow_record(resource_id: uuid::Uuid, resource_name: &str) -> AdmissionRecord {
        AdmissionRecord {
            resource_id: resource_id.to_string(),
            resource_name: resource_name.to_string(),
            policy_version: 7,
            decision: "ALLOW".to_string(),
            restriction_reason: None,
            effective_at_unix_seconds: 1_700_000_000,
            valid_until_unix_seconds: None,
            source_event_id: uuid::Uuid::new_v4().to_string(),
        }
    }

    #[test]
    fn serializes_the_zone_admission_contract() {
        let record = AdmissionRecord {
            resource_id: uuid::Uuid::new_v4().to_string(),
            resource_name: "personal-bucket".to_string(),
            policy_version: 7,
            decision: "SUSPEND_BILLABLE".to_string(),
            restriction_reason: Some("wallet_depleted".to_string()),
            effective_at_unix_seconds: 1_700_000_000,
            valid_until_unix_seconds: Some(1_700_003_600),
            source_event_id: uuid::Uuid::new_v4().to_string(),
        };

        let value = serde_json::to_value(record).expect("admission record must serialize");
        assert_eq!(value["policy_version"], 7);
        assert_eq!(value["decision"], "SUSPEND_BILLABLE");
        assert_eq!(value["restriction_reason"], "wallet_depleted");
        assert!(value.get("source_event_id").is_some());
        assert!(value.get("wallet_version").is_none());
        assert!(value.get("admission_mode").is_none());
    }

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

    #[tokio::test]
    #[ignore = "requires dedicated AURORA_TEST_NATS"]
    async fn equal_version_conflict_fails_closed() {
        let zone_kv = crate::infra::zone_kv::ZoneKvStore::for_test().await;
        let resource_id = uuid::Uuid::new_v4();
        let first = allow_record(resource_id, "ws-12345678-archive");
        apply_admission_to_kv(zone_kv.admission(), &first)
            .await
            .unwrap();

        let mut conflicting = first.clone();
        conflicting.decision = "SUSPEND_BILLABLE".to_string();
        conflicting.restriction_reason = Some("CREDIT_EXHAUSTED".to_string());
        assert!(apply_admission_to_kv(zone_kv.admission(), &conflicting)
            .await
            .is_err());
    }

    #[tokio::test]
    #[ignore = "requires dedicated AURORA_TEST_NATS"]
    async fn cleanup_allows_the_same_name_to_bind_a_new_resource() {
        let zone_kv = crate::infra::zone_kv::ZoneKvStore::for_test().await;
        let resource_name = "ws-12345678-archive";
        let first = allow_record(uuid::Uuid::new_v4(), resource_name);
        apply_admission_to_kv(zone_kv.admission(), &first)
            .await
            .unwrap();
        remove_admission_from_kv(zone_kv.admission(), &first.resource_id, resource_name)
            .await
            .unwrap();

        let second = allow_record(uuid::Uuid::new_v4(), resource_name);
        apply_admission_to_kv(zone_kv.admission(), &second)
            .await
            .unwrap();
        let entry = zone_kv
            .admission()
            .entry(format!("name/{resource_name}"))
            .await
            .unwrap()
            .unwrap();
        let stored: AdmissionRecord = serde_json::from_slice(&entry.value).unwrap();
        assert_eq!(stored.resource_id, second.resource_id);
    }

    #[tokio::test]
    #[ignore = "requires dedicated AURORA_TEST_NATS"]
    async fn stale_delivery_repairs_a_partial_newer_projection() {
        let zone_kv = crate::infra::zone_kv::ZoneKvStore::for_test().await;
        let resource_id = uuid::Uuid::new_v4();
        let resource_name = "ws-12345678-archive";
        let mut newer = allow_record(resource_id, resource_name);
        newer.policy_version = 9;
        let newer_value = Bytes::from(serde_json::to_vec(&newer).unwrap());
        zone_kv
            .admission()
            .update(format!("name/{resource_name}"), newer_value, 0)
            .await
            .unwrap();

        let mut stale = newer.clone();
        stale.policy_version = 8;
        stale.source_event_id = uuid::Uuid::new_v4().to_string();
        apply_admission_to_kv(zone_kv.admission(), &stale)
            .await
            .unwrap();

        for key in [format!("name/{resource_name}"), resource_id.to_string()] {
            let entry = zone_kv.admission().entry(key).await.unwrap().unwrap();
            let stored: AdmissionRecord = serde_json::from_slice(&entry.value).unwrap();
            assert_eq!(stored, newer);
        }
    }
}
