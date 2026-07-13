use crate::executor::storage::core::MinioAdminClient;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;

use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm thay đổi hạn mức quota vật lý của Bucket trên MinIO.
pub struct BucketResizeExecutor;

#[async_trait]
impl Executor for BucketResizeExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.resize";

        // [COMMENT]: 1. Giải mã (Decode) payload nhị phân từ Protobuf sang struct BucketResizeSync
        let sync_data = match storage_proto::BucketResizeSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketResizeSync payload: {}",
                    e
                )));
            }
        };

        // [COMMENT]: Validate dữ liệu bắt buộc
        if sync_data.name.is_empty() || sync_data.requested_quota_bytes <= 0 {
            return Err(ExecutorError::ExecutionFailed(
                "BucketResizeSync payload missing required fields (name / requested_quota_bytes)".to_string(),
            ));
        }

        Logger::sys_info(
            op,
            &format!(
                "Resize bucket '{}' (ID: {}): {} -> {} bytes",
                sync_data.name, sync_data.bucket_id, sync_data.current_quota_bytes, sync_data.requested_quota_bytes
            ),
        );

        let admin_client = MinioAdminClient::from_env();

        // [COMMENT]: 2. Gọi mc admin để update hard quota trên bucket
        admin_client
            .set_bucket_quota(&sync_data.name, sync_data.requested_quota_bytes)
            .await
            .map_err(|e| {
                ExecutorError::ExecutionFailed(format!(
                    "Failed to set bucket quota on MinIO for bucket '{}': {}",
                    sync_data.name, e
                ))
            })?;

        Logger::sys_info(
            op,
            &format!("BucketResizeExecutor OK: Bucket '{}' resized successfully.", sync_data.name),
        );

        Ok(ExecutionResult {
            message: format!("Bucket '{}' resized successfully", sync_data.name),
        })
    }
}
