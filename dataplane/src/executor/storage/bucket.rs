use async_trait::async_trait;
use prost::Message;

use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::executor::storage::core::MinioClient;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;

// [COMMENT]: Import Struct được tự động sinh ra bởi prost_build từ storage_job.proto
pub mod storage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

/// [COMMENT]: Executor chịu trách nhiệm khởi tạo vật lý Storage Bucket trên cụm MinIO.
pub struct BucketCreateExecutor;

#[async_trait]
impl Executor for BucketCreateExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.executor.create";

        // 1. Giải mã (Decode) payload nhị phân từ Protobuf sang struct BucketSync
        let sync_data = match storage_proto::BucketSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketSync protobuf payload: {}",
                    e
                )));
            }
        };

        Logger::sys_info(
            op,
            &format!(
                "BucketCreateExecutor: Khởi chạy khởi tạo bucket vật lý. Name: {}, Zone: {}, Quota: {} bytes",
                sync_data.name, sync_data.zone_id, sync_data.capacity_quota_bytes
            ),
        );

        // 2. Khởi tạo MinIO client từ môi trường (HA Zone config)
        let minio_client = MinioClient::from_env();

        // 3. Thực hiện gửi yêu cầu tạo bucket vật lý
        let response_res = minio_client.create_bucket(&sync_data.name).await;

        match response_res {
            Ok(resp) => {
                let status = resp.status();
                if status.is_success() {
                    Logger::sys_info(
                        op,
                        &format!("BucketCreateExecutor: Khởi tạo bucket '{}' thành công. HTTP Status: {}", sync_data.name, status),
                    );
                    Ok(ExecutionResult {
                        message: format!("Bucket '{}' physically provisioned on MinIO", sync_data.name),
                    })
                } else if status == reqwest::StatusCode::CONFLICT {
                    // Idempotency: Nếu bucket đã tồn tại từ trước, coi như xử lý thành công (Idempotent Success)
                    Logger::sys_info(
                        op,
                        &format!("BucketCreateExecutor: Bucket '{}' đã tồn tại từ trước (409 Conflict). Bỏ qua và báo thành công.", sync_data.name),
                    );
                    Ok(ExecutionResult {
                        message: format!("Bucket '{}' already exists, marked as success (idempotent)", sync_data.name),
                    })
                } else {
                    let err_msg = format!("MinIO rejected bucket creation. HTTP Status: {}", status);
                    Logger::sys_error(op, &err_msg, "MINIO_REJECTED");
                    Err(ExecutorError::ExecutionFailed(err_msg))
                }
            }
            Err(e) => {
                let err_msg = format!("Failed to connect to MinIO cluster for bucket '{}': {}", sync_data.name, e);
                Logger::sys_error(op, &err_msg, "MINIO_CONNECTION_FAILED");
                Err(ExecutorError::ExecutionFailed(err_msg))
            }
        }
    }
}
