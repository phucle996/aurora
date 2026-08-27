use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm xóa sạch Bucket vật lý cùng các Credentials liên kết trên MinIO.
pub struct BucketDeleteExecutor;

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
