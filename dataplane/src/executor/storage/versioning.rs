use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

use super::storage_proto;

/// Executor chịu trách nhiệm cấu hình bật/tạm dừng Object Versioning trên MinIO.
pub struct BucketVersioningExecutor;

#[async_trait]
impl Executor for BucketVersioningExecutor {
    async fn execute(&self, payload: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.versioning";

        let sync_data = match storage_proto::BucketVersioningSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketVersioningSync payload: {}",
                    e
                )));
            }
        };

        if sync_data.name.is_empty() || sync_data.bucket_id != payload.resource_id {
            return Err(ExecutorError::ExecutionFailed(
                "BucketVersioningSync payload missing required field: name".to_string(),
            ));
        }

        let state_str = if sync_data.versioning_enabled {
            "Enabled"
        } else {
            "Suspended"
        };
        Logger::sys_info(
            op,
            &format!(
                "Setting versioning for bucket '{}' (ID: {}): {}",
                sync_data.name, sync_data.bucket_id, state_str
            ),
        );

        let s3_client = MinioClient::from_env_private().await;
        if let Err(e) = s3_client
            .put_bucket_versioning(&sync_data.name, sync_data.versioning_enabled)
            .await
        {
            Logger::sys_warn(
                op,
                &format!(
                    "S3 PutBucketVersioning failed for '{}': {}, trying mc CLI fallback...",
                    sync_data.name, e
                ),
                "S3_PUT_VERSIONING_FAILED",
            );
            let admin_client = MinioAdminClient::from_env();
            admin_client
                .set_bucket_versioning(&sync_data.name, sync_data.versioning_enabled)
                .await
                .map_err(|err| {
                    ExecutorError::ExecutionFailed(format!(
                        "Failed to set versioning on MinIO for bucket '{}': {}",
                        sync_data.name, err
                    ))
                })?;
        }

        Logger::sys_info(
            op,
            &format!(
                "BucketVersioningExecutor OK: Bucket '{}' versioning set to {}.",
                sync_data.name, state_str
            ),
        );

        let result = storage_proto::BucketVersioningAppliedV1 {
            schema_version: 1,
            bucket_id: sync_data.bucket_id,
            actual_versioning_enabled: sync_data.versioning_enabled,
        };
        Ok(ExecutionResult {
            message: format!(
                "Bucket '{}' versioning set to {}",
                sync_data.name, state_str
            ),
            result_payload: result.encode_to_vec(),
            result_payload_schema_version: 1,
        })
    }
}
