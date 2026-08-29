use std::sync::Arc;

use bytes::Bytes;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::executor::ExecutorError;
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::model::ValidatedJob;

#[derive(Clone, Debug)]
pub struct StorageBucketRegistration {
    pub bucket_id: Uuid,
    pub bucket_name: String,
    pub owner_id: Uuid,
    pub owner_type: String,
    pub workspace_id: Uuid,
    pub zone_id: Uuid,
    pub event_id: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
struct StorageBucketRuntimeHead {
    schema_version: u32,
    runtime_read_enabled: bool,
    module: String,
    resource_type: String,
    resource_id: String,
    resource_name: String,
    version: u64,
    event_id: String,
    tombstoned: bool,
    owner_id: String,
    owner_type: String,
    workspace_id: String,
    zone_id: String,
}

impl StorageBucketRegistration {
    pub fn validate(&self, job: &ValidatedJob) -> Result<(), ExecutorError> {
        let expected_prefix = match self.owner_type.as_str() {
            "PERSONAL" => "ws-",
            "TENANT" => "tn-",
            _ => {
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_RUNTIME_OWNER_TYPE_INVALID".into(),
                ));
            }
        };
        if self.bucket_id.is_nil()
            || self.owner_id.is_nil()
            || self.workspace_id.is_nil()
            || self.zone_id.is_nil()
            || self.bucket_id.to_string() != job.resource_id
            || self.zone_id.to_string() != job.target_zone_id
            || self.bucket_name.is_empty()
            || self.bucket_name.len() > 63
            || !self.bucket_name.starts_with(expected_prefix)
            || !self
                .bucket_name
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        {
            return Err(ExecutorError::ExecutionFailed(
                "STORAGE_RUNTIME_REGISTRATION_INVALID".into(),
            ));
        }
        Ok(())
    }

    pub async fn activate(&self, store: Arc<ZoneKvStore>) -> Result<(), ExecutorError> {
        let key = format!("storage.bucket.head.{}", self.bucket_id);
        let desired = StorageBucketRuntimeHead {
            schema_version: 1,
            runtime_read_enabled: true,
            module: "storage".into(),
            resource_type: "bucket".into(),
            resource_id: self.bucket_id.to_string(),
            resource_name: self.bucket_name.clone(),
            version: 1,
            event_id: self.event_id.clone(),
            tombstoned: false,
            owner_id: self.owner_id.to_string(),
            owner_type: self.owner_type.clone(),
            workspace_id: self.workspace_id.to_string(),
            zone_id: self.zone_id.to_string(),
        };
        let value = Bytes::from(serde_json::to_vec(&desired).map_err(|_| {
            ExecutorError::ExecutionFailed("STORAGE_RUNTIME_HEAD_ENCODE_FAILED".into())
        })?);
        for _ in 0..5 {
            let current = store
                .config_entry(&key)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
            if let Some(entry) = current {
                let existing: StorageBucketRuntimeHead = serde_json::from_slice(&entry.value)
                    .map_err(|_| {
                        ExecutorError::ExecutionFailed("STORAGE_RUNTIME_HEAD_CORRUPT".into())
                    })?;
                if existing == desired {
                    return Ok(());
                }
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_RUNTIME_HEAD_CONFLICT".into(),
                ));
            }
            if store.config_create(&key, value.clone()).await.is_ok() {
                return Ok(());
            }
        }
        Err(ExecutorError::Retryable(
            "STORAGE_RUNTIME_HEAD_CAS_CONTENTION".into(),
        ))
    }

    pub async fn tombstone(&self, store: Arc<ZoneKvStore>) -> Result<(), ExecutorError> {
        let key = format!("storage.bucket.head.{}", self.bucket_id);
        for _ in 0..5 {
            let current = store
                .config_entry(&key)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
            let (revision, version) = if let Some(entry) = &current {
                let existing: StorageBucketRuntimeHead = serde_json::from_slice(&entry.value)
                    .map_err(|_| {
                        ExecutorError::ExecutionFailed("STORAGE_RUNTIME_HEAD_CORRUPT".into())
                    })?;
                if existing.module != "storage"
                    || existing.resource_type != "bucket"
                    || existing.resource_id != self.bucket_id.to_string()
                    || existing.resource_name != self.bucket_name
                    || existing.owner_id != self.owner_id.to_string()
                    || existing.owner_type != self.owner_type
                    || existing.workspace_id != self.workspace_id.to_string()
                    || existing.zone_id != self.zone_id.to_string()
                {
                    return Err(ExecutorError::ExecutionFailed(
                        "STORAGE_RUNTIME_HEAD_CONFLICT".into(),
                    ));
                }
                if existing.tombstoned && !existing.runtime_read_enabled {
                    return Ok(());
                }
                (entry.revision, existing.version.saturating_add(1).max(1))
            } else {
                (0, 1)
            };
            let desired = StorageBucketRuntimeHead {
                schema_version: 1,
                runtime_read_enabled: false,
                module: "storage".into(),
                resource_type: "bucket".into(),
                resource_id: self.bucket_id.to_string(),
                resource_name: self.bucket_name.clone(),
                version,
                event_id: self.event_id.clone(),
                tombstoned: true,
                owner_id: self.owner_id.to_string(),
                owner_type: self.owner_type.clone(),
                workspace_id: self.workspace_id.to_string(),
                zone_id: self.zone_id.to_string(),
            };
            let value = Bytes::from(serde_json::to_vec(&desired).map_err(|_| {
                ExecutorError::ExecutionFailed("STORAGE_RUNTIME_HEAD_ENCODE_FAILED".into())
            })?);
            let result = if revision == 0 {
                store.config_create(&key, value).await
            } else {
                store.config_update(&key, value, revision).await
            };
            if result.is_ok() {
                return Ok(());
            }
        }
        Err(ExecutorError::Retryable(
            "STORAGE_RUNTIME_HEAD_CAS_CONTENTION".into(),
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn registration_binds_outer_resource_zone_and_owner_namespace() {
        let bucket_id = Uuid::new_v4();
        let job = crate::job_runtime::test::validated_job(
            "STORAGE",
            "storage.bucket.create",
            &bucket_id.to_string(),
            &[1],
        );
        let mut registration = StorageBucketRegistration {
            bucket_id,
            bucket_name: "ws-12345678-backups".into(),
            owner_id: Uuid::new_v4(),
            owner_type: "PERSONAL".into(),
            workspace_id: Uuid::new_v4(),
            zone_id: Uuid::parse_str(&job.target_zone_id).unwrap(),
            event_id: job.job_id.clone(),
        };
        assert!(registration.validate(&job).is_ok());

        registration.bucket_name = "tn-12345678-backups".into();
        assert!(registration.validate(&job).is_err());
        registration.bucket_name = "ws-12345678-backups".into();
        registration.zone_id = Uuid::new_v4();
        assert!(registration.validate(&job).is_err());
    }
}
