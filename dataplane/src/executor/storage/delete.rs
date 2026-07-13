use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;

use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm xóa sạch Bucket vật lý cùng các Credentials liên kết trên MinIO.
pub struct BucketDeleteExecutor;

#[async_trait]
impl Executor for BucketDeleteExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
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
                ExecutorError::ExecutionFailed(format!(
                    "Failed to delete bucket '{}' on MinIO: {}",
                    sync_data.name, e
                ))
            })?;

        // [COMMENT]: Step 2: Xóa sạch tất cả credentials và policies liên kết trên MinIO (idempotent, bỏ qua lỗi)
        for access_key in &sync_data.access_keys {
            let policy_name = format!("policy-{}", access_key);

            Logger::sys_info(op, &format!("Xóa user '{}' trên MinIO...", access_key));
            if let Err(e) = admin_client.delete_user(access_key).await {
                Logger::sys_error(op, &format!("Xóa user '{}' thất bại", access_key), &e);
            }

            Logger::sys_info(op, &format!("Xóa policy '{}' trên MinIO...", policy_name));
            if let Err(e) = admin_client.delete_policy(&policy_name).await {
                Logger::sys_error(op, &format!("Xóa policy '{}' thất bại", policy_name), &e);
            }
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
        })
    }
}
