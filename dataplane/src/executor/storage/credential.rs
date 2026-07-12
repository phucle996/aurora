use crate::executor::storage::core::MinioAdminClient;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;

// [COMMENT]: Import Struct được tự động sinh ra bởi prost_build từ storage_job.proto
pub mod storage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

/// [COMMENT]: Executor chịu trách nhiệm khởi tạo vật lý Access Key trên cụm MinIO.
pub struct CredentialCreateExecutor;

#[async_trait]
impl Executor for CredentialCreateExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.credential.create";

        // 1. Giải mã (Decode) payload nhị phân từ Protobuf sang struct CredentialSync
        let sync_data = match storage_proto::CredentialSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode CredentialSync protobuf payload: {}",
                    e
                )));
            }
        };

        // Validate các trường dữ liệu bắt buộc
        if sync_data.access_key.is_empty() || sync_data.secret_key.is_empty() || sync_data.policy.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "CredentialSync payload missing required fields (access_key / secret_key / policy)".to_string(),
            ));
        }

        Logger::sys_info(
            op,
            &format!(
                "CredentialCreateExecutor: Bắt đầu tạo Access Key '{}' cho Bucket ID '{}'",
                sync_data.access_key, sync_data.bucket_id
            ),
        );

        // 2. Khởi tạo MinIO Admin client
        let admin_client = MinioAdminClient::from_env();

        // STEP 1: Tạo MinIO User với access_key + secret_key
        Logger::sys_info(op, &format!("Step 1/3: Tạo MinIO user '{}'...", sync_data.access_key));
        admin_client
            .create_user(&sync_data.access_key, &sync_data.secret_key)
            .await
            .map_err(|e| ExecutorError::ExecutionFailed(format!("Failed to create MinIO user: {}", e)))?;

        // STEP 2: Tạo policy trên MinIO Admin API
        Logger::sys_info(op, &format!("Step 2/3: Tạo policy cho user '{}'...", sync_data.access_key));
        let policy_name = format!("policy-{}", sync_data.access_key);
        admin_client
            .set_user_bucket_policy(&policy_name, &sync_data.policy)
            .await
            .map_err(|e| {
                ExecutorError::ExecutionFailed(format!("Failed to set user bucket policy: {}", e))
            })?;

        // STEP 3: Gắn policy vào user
        Logger::sys_info(op, &format!("Step 3/3: Gắn policy '{}' vào user '{}'...", policy_name, sync_data.access_key));
        admin_client
            .attach_policy_to_user(&sync_data.access_key, &policy_name)
            .await
            .map_err(|e| ExecutorError::ExecutionFailed(format!("Failed to attach policy: {}", e)))?;

        Logger::sys_info(
            op,
            &format!("CredentialCreateExecutor OK: Access Key '{}' được kích hoạt thành công.", sync_data.access_key),
        );

        Ok(ExecutionResult {
            message: format!("Credential '{}' created successfully", sync_data.access_key),
        })
    }
}

/// [COMMENT]: Executor chịu trách nhiệm xóa vật lý Access Key khỏi cụm MinIO.
pub struct CredentialDeleteExecutor;

#[async_trait]
impl Executor for CredentialDeleteExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.credential.delete";

        // 1. Giải mã (Decode) payload nhị phân từ Protobuf sang struct CredentialSync
        let sync_data = match storage_proto::CredentialSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode CredentialSync protobuf payload: {}",
                    e
                )));
            }
        };

        if sync_data.access_key.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "CredentialSync payload missing access_key for deletion".to_string(),
            ));
        }

        Logger::sys_info(
            op,
            &format!(
                "CredentialDeleteExecutor: Bắt đầu xóa Access Key '{}'...",
                sync_data.access_key
            ),
        );

        // 2. Khởi tạo MinIO Admin client
        let admin_client = MinioAdminClient::from_env();
        let policy_name = format!("policy-{}", sync_data.access_key);

        // STEP 1: Xóa user trên MinIO
        Logger::sys_info(op, &format!("Step 1/2: Xóa MinIO user '{}'...", sync_data.access_key));
        admin_client
            .delete_user(&sync_data.access_key)
            .await
            .map_err(|e| ExecutorError::ExecutionFailed(format!("Failed to delete MinIO user: {}", e)))?;

        // STEP 2: Xóa policy tương ứng
        Logger::sys_info(op, &format!("Step 2/2: Xóa policy '{}'...", policy_name));
        admin_client
            .delete_policy(&policy_name)
            .await
            .map_err(|e| ExecutorError::ExecutionFailed(format!("Failed to delete policy: {}", e)))?;

        Logger::sys_info(
            op,
            &format!("CredentialDeleteExecutor OK: Access Key '{}' đã bị xóa.", sync_data.access_key),
        );

        Ok(ExecutionResult {
            message: format!("Credential '{}' deleted successfully", sync_data.access_key),
        })
    }
}
