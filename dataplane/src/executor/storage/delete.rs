use crate::executor::storage::commercial_admission::remove_admission_from_kv;
use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::storage::runtime_registration::StorageBucketRegistration;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm xóa sạch Bucket vật lý cùng các Credentials liên kết trên MinIO.
pub struct BucketDeleteExecutor {
    zone_kv: Arc<ZoneKvStore>,
}

impl BucketDeleteExecutor {
    pub fn new(zone_kv: Arc<ZoneKvStore>) -> Self {
        Self { zone_kv }
    }
}

#[async_trait]
impl Executor for BucketDeleteExecutor {
    async fn execute(&self, payload: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.delete";

        // [COMMENT]: 1. Giải mã (Decode) payload nhị phân sang struct BucketDeleteSync
        let sync_data = match storage_proto::BucketDeleteSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketDeleteSync payload: {}",
                    e
                )));
            }
        };

        if sync_data.name.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "BucketDeleteSync payload missing required bucket name".to_string(),
            ));
        }

        if payload.payload_schema_version != 1 || sync_data.schema_version > 1 {
            return Err(ExecutorError::ExecutionFailed(
                "STORAGE_BUCKET_DELETE_SCHEMA_INVALID".into(),
            ));
        }
        let registration = if sync_data.schema_version == 1 {
            let registration = StorageBucketRegistration {
                bucket_id: uuid::Uuid::from_slice(&sync_data.bucket_id).map_err(|_| {
                    ExecutorError::ExecutionFailed("STORAGE_BUCKET_ID_INVALID".into())
                })?,
                bucket_name: sync_data.name.clone(),
                owner_id: uuid::Uuid::from_slice(&sync_data.owner_id).map_err(|_| {
                    ExecutorError::ExecutionFailed("STORAGE_OWNER_ID_INVALID".into())
                })?,
                owner_type: sync_data.owner_type.clone(),
                workspace_id: uuid::Uuid::from_slice(&sync_data.workspace_id).map_err(|_| {
                    ExecutorError::ExecutionFailed("STORAGE_WORKSPACE_ID_INVALID".into())
                })?,
                zone_id: uuid::Uuid::from_slice(&sync_data.zone_id).map_err(|_| {
                    ExecutorError::ExecutionFailed("STORAGE_ZONE_ID_INVALID".into())
                })?,
                event_id: payload.job_id.clone(),
            };
            registration.validate(&payload)?;
            Some(registration)
        } else {
            None
        };

        Logger::sys_info(
            op,
            &format!(
                "Bắt đầu xóa bucket '{}' và {} credentials liên kết...",
                sync_data.name,
                sync_data.access_keys.len()
            ),
        );

        let admin_client = MinioAdminClient::from_env();
        let s3_client = MinioClient::from_env_private().await;

        // [COMMENT]: Step 1: Xóa bucket vật lý trước. Nếu bucket không rỗng hoặc có lỗi kết nối,
        // lệnh sẽ dừng lại ngay lập tức và báo lỗi. Tránh tình trạng xóa mất key truy cập nhưng bucket vẫn tồn tại.
        Logger::sys_info(
            op,
            &format!("Xóa bucket vật lý '{}' trên MinIO...", sync_data.name),
        );
        s3_client
            .delete_bucket(&sync_data.name)
            .await
            .map_err(|e| {
                ExecutorError::OutcomeUnknown(format!(
                    "Failed to delete bucket '{}' on MinIO: {}",
                    sync_data.name, e
                ))
            })?;

        // Every owned credential and policy is part of deletion. Only a typed
        // already-absent result is idempotent; infrastructure errors must retry.
        for access_key in &sync_data.access_keys {
            let policy_name = format!("policy-{}", access_key);

            Logger::sys_info(op, &format!("Xóa user '{}' trên MinIO...", access_key));
            admin_client
                .delete_user(access_key)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;

            Logger::sys_info(op, &format!("Xóa policy '{}' trên MinIO...", policy_name));
            admin_client
                .delete_policy(&policy_name)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
        }

        Logger::sys_info(
            op,
            &format!(
                "BucketDeleteExecutor OK: Bucket '{}' đã được xóa hoàn toàn.",
                sync_data.name
            ),
        );

        // The tombstone is part of physical deletion settlement. If Zone KV
        // is unavailable, retrying is safe because MinIO NoSuchBucket is
        // treated as an idempotent delete result.
        if let Some(registration) = registration {
            registration.tombstone(self.zone_kv.clone()).await?;
            remove_admission_from_kv(
                self.zone_kv.admission(),
                &registration.bucket_id.to_string(),
                &registration.bucket_name,
            )
            .await?;
        }

        Ok(ExecutionResult {
            message: format!(
                "Bucket '{}' and associated credentials deleted successfully",
                sync_data.name
            ),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        })
    }
}
