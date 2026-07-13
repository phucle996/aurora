use async_trait::async_trait;
use prost::Message;

use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;

// [COMMENT]: Sử dụng Struct được tự động sinh ra bởi prost_build từ parent module storage_proto
use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm khởi tạo vật lý Storage Bucket trên cụm MinIO.
/// Thực hiện 3 bước nguyên tử theo thứ tự:
///   1. Tạo bucket vật lý (S3 SDK, Signature V4)
///   2. Tạo MinIO user với access_key + secret_key từ payload (Admin API)
///   3. Gán bucket policy cho user đó (S3 SDK put_bucket_policy)
/// Mỗi bước đều idempotent → toàn bộ job an toàn khi retry.
pub struct BucketCreateExecutor;

#[async_trait]
impl Executor for BucketCreateExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.create";

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

        // [COMMENT]: Validate credential fields — bắt buộc phải có đủ thông tin credential
        if sync_data.access_key.is_empty() || sync_data.secret_key.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "BucketSync payload missing credential fields (access_key / secret_key)"
                    .to_string(),
            ));
        }

        Logger::sys_info(
            op,
            &format!(
                "BucketCreateExecutor: Khởi chạy provisioning bucket. Name: {}, AccessKey: {}",
                sync_data.name, sync_data.access_key
            ),
        );

        // 2. Khởi tạo MinIO client (S3 SDK) và Admin client
        let minio_client = MinioClient::from_env().await;
        let admin_client = MinioAdminClient::from_env();

        // ─────────────────────────────────────────────────────────────────────
        // STEP 1: Tạo bucket vật lý (idempotent — BucketAlreadyExists = success)
        // ─────────────────────────────────────────────────────────────────────
        Logger::sys_info(
            op,
            &format!("Step 1/3: Tạo bucket vật lý '{}'...", sync_data.name),
        );
        match minio_client.create_bucket(&sync_data.name).await {
            Ok(_) => {
                Logger::sys_info(
                    op,
                    &format!("Step 1/3 OK: Bucket '{}' tạo thành công.", sync_data.name),
                );
            }
            Err(e) => {
                let err_str = e.to_string();
                // [COMMENT]: Idempotency — bucket đã tồn tại = success
                if err_str.contains("BucketAlreadyExists")
                    || err_str.contains("BucketAlreadyOwnedByYou")
                {
                    Logger::sys_info(
                        op,
                        &format!(
                            "Step 1/3 SKIP: Bucket '{}' đã tồn tại (idempotent).",
                            sync_data.name
                        ),
                    );
                } else {
                    let msg = format!(
                        "Step 1/3 FAIL: Không thể tạo bucket '{}': {}",
                        sync_data.name, e
                    );
                    Logger::sys_error(op, &msg, "BUCKET_CREATE_FAILED");
                    return Err(ExecutorError::ExecutionFailed(msg));
                }
            }
        }

        // ─────────────────────────────────────────────────────────────────────
        // STEP 2: Tạo MinIO User với access_key + secret_key từ payload
        // (idempotent — user đã tồn tại = success)
        // ─────────────────────────────────────────────────────────────────────
        Logger::sys_info(
            op,
            &format!("Step 2/3: Tạo MinIO user '{}'...", sync_data.access_key),
        );
        if let Err(e) = admin_client
            .create_user(&sync_data.access_key, &sync_data.secret_key)
            .await
        {
            let msg = format!(
                "Step 2/3 FAIL: Không thể tạo MinIO user '{}': {}",
                sync_data.access_key, e
            );
            Logger::sys_error(op, &msg, "MINIO_USER_CREATE_FAILED");

            // [COMMENT]: Rollback Step 1: Xóa bucket vừa tạo do không thể tạo user tương ứng
            Logger::sys_info(
                op,
                &format!("Rollback Step 1: Đang xóa bucket '{}'...", sync_data.name),
            );
            if let Err(rollback_err) = minio_client.delete_bucket(&sync_data.name).await {
                Logger::sys_error(
                    op,
                    &format!(
                        "Rollback Step 1 FAIL: Không thể xóa bucket '{}': {}",
                        sync_data.name, rollback_err
                    ),
                    "ROLLBACK_FAILED",
                );
            }
            return Err(ExecutorError::ExecutionFailed(msg));
        }
        Logger::sys_info(
            op,
            &format!(
                "Step 2/3 OK: MinIO user '{}' đã sẵn sàng.",
                sync_data.access_key
            ),
        );

        // ─────────────────────────────────────────────────────────────────────
        // STEP 3: Tạo policy và gắn bucket policy giới hạn quyền của user này
        // vào đúng bucket đó (1 user : 1 bucket = scope tối thiểu)
        // ─────────────────────────────────────────────────────────────────────
        Logger::sys_info(
            op,
            &format!(
                "Step 3/3: Gán bucket policy cho user '{}'...",
                sync_data.access_key
            ),
        );

        // [COMMENT]: Dùng access_key làm policy name để đảm bảo unique và traceable
        let policy_name = format!("policy-{}", sync_data.access_key);

        // [COMMENT]: Tạo policy JSON S3-compatible (giới hạn đúng bucket này)
        let bucket_policy_json = format!(
            r#"{{
            "Version": "2012-10-17",
            "Statement": [{{
                "Effect": "Allow",
                "Principal": {{"AWS": ["arn:aws:iam:::user/{access_key}"]}},
                "Action": ["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:ListBucket"],
                "Resource": ["arn:aws:s3:::{bucket}","arn:aws:s3:::{bucket}/*"]
            }}]
        }}"#,
            access_key = sync_data.access_key,
            bucket = sync_data.name,
        );

        // [COMMENT]: Tạo policy trên MinIO Admin API
        if let Err(e) = admin_client
            .set_user_bucket_policy(&policy_name, &bucket_policy_json)
            .await
        {
            let msg = format!("Step 3/3 FAIL (set_policy): {}", e);
            Logger::sys_error(op, &msg, "MINIO_POLICY_CREATE_FAILED");

            // [COMMENT]: Rollback Step 2: Xóa user vừa tạo ở Step 2
            Logger::sys_info(
                op,
                &format!(
                    "Rollback Step 2: Đang xóa user '{}'...",
                    sync_data.access_key
                ),
            );
            let _ = admin_client.delete_user(&sync_data.access_key).await;

            // [COMMENT]: Rollback Step 1: Xóa bucket ở Step 1
            Logger::sys_info(
                op,
                &format!("Rollback Step 1: Đang xóa bucket '{}'...", sync_data.name),
            );
            let _ = minio_client.delete_bucket(&sync_data.name).await;

            return Err(ExecutorError::ExecutionFailed(msg));
        }

        // [COMMENT]: Gắn policy vào user
        if let Err(e) = admin_client
            .attach_policy_to_user(&sync_data.access_key, &policy_name)
            .await
        {
            let msg = format!("Step 3/3 FAIL (attach_policy): {}", e);
            Logger::sys_error(op, &msg, "MINIO_POLICY_ATTACH_FAILED");

            // [COMMENT]: Rollback Step 3: Xóa policy vừa tạo
            Logger::sys_info(
                op,
                &format!("Rollback Step 3: Đang xóa policy '{}'...", policy_name),
            );
            let _ = admin_client.delete_policy(&policy_name).await;

            // [COMMENT]: Rollback Step 2: Xóa user
            Logger::sys_info(
                op,
                &format!(
                    "Rollback Step 2: Đang xóa user '{}'...",
                    sync_data.access_key
                ),
            );
            let _ = admin_client.delete_user(&sync_data.access_key).await;

            // [COMMENT]: Rollback Step 1: Xóa bucket
            Logger::sys_info(
                op,
                &format!("Rollback Step 1: Đang xóa bucket '{}'...", sync_data.name),
            );
            let _ = minio_client.delete_bucket(&sync_data.name).await;

            return Err(ExecutorError::ExecutionFailed(msg));
        }

        Logger::sys_info(
            op,
            &format!(
                "Step 3/3 OK: Bucket policy gắn vào user '{}'. Provisioning hoàn tất.",
                sync_data.access_key
            ),
        );

        Ok(ExecutionResult {
            message: format!(
                "Bucket '{}' provisioned với access key '{}' (user + policy + bucket)",
                sync_data.name, sync_data.access_key
            ),
        })
    }
}
