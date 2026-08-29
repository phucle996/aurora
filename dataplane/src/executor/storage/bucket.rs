use async_trait::async_trait;
use prost::Message;

use crate::executor::storage::commercial_admission::{apply_admission_to_kv, AdmissionRecord};
use crate::executor::storage::core::{MinioAdminClient, MinioClient};
use crate::executor::storage::runtime_registration::StorageBucketRegistration;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use std::sync::Arc;

// [COMMENT]: Sử dụng Struct được tự động sinh ra bởi prost_build từ parent module storage_proto
use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm khởi tạo vật lý Storage Bucket trên cụm MinIO.
/// Thực hiện 3 bước nguyên tử theo thứ tự:
///   1. Tạo bucket vật lý (S3 SDK, Signature V4)
///   2. Tạo MinIO user với access_key + secret_key từ payload (Admin API)
///   3. Gán bucket policy cho user đó (S3 SDK put_bucket_policy)
///
/// Mỗi bước đều idempotent → toàn bộ job an toàn khi retry.
pub struct BucketCreateExecutor {
    zone_kv: Arc<ZoneKvStore>,
}

impl BucketCreateExecutor {
    pub fn new(zone_kv: Arc<ZoneKvStore>) -> Self {
        Self { zone_kv }
    }
}

#[async_trait]
impl Executor for BucketCreateExecutor {
    async fn execute(&self, payload: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.create";

        // 1. Giải mã (Decode) payload nhị phân từ Protobuf sang struct BucketCreateSync
        let sync_data = match storage_proto::BucketCreateSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketCreateSync protobuf payload: {}",
                    e
                )));
            }
        };

        if payload.payload_schema_version != 1 || sync_data.schema_version > 2 {
            return Err(ExecutorError::ExecutionFailed(
                "STORAGE_BUCKET_CREATE_SCHEMA_INVALID".into(),
            ));
        }
        // Schema 0 is the rolling-upgrade shape emitted before runtime
        // registration existed. It may finish provisioning, but missing
        // owner/workspace facts can never be inferred into an authority head.
        let registration = if sync_data.schema_version >= 1 {
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
        let admission = if sync_data.schema_version == 2 {
            let effective_at =
                chrono::DateTime::parse_from_rfc3339(&sync_data.admission_effective_at)
                    .map(|value| value.timestamp())
                    .map_err(|_| {
                        ExecutorError::ExecutionFailed(
                            "STORAGE_CREATE_ADMISSION_EFFECTIVE_AT_INVALID".into(),
                        )
                    })?;
            let valid_until = if sync_data.admission_valid_until.is_empty() {
                None
            } else {
                let value = chrono::DateTime::parse_from_rfc3339(&sync_data.admission_valid_until)
                    .map(|value| value.timestamp())
                    .map_err(|_| {
                        ExecutorError::ExecutionFailed(
                            "STORAGE_CREATE_ADMISSION_VALID_UNTIL_INVALID".into(),
                        )
                    })?;
                if value <= effective_at {
                    return Err(ExecutorError::ExecutionFailed(
                        "STORAGE_CREATE_ADMISSION_WINDOW_INVALID".into(),
                    ));
                }
                Some(value)
            };
            if sync_data.admission_policy_version <= 0
                || sync_data.admission_decision != "ALLOW"
                || !sync_data.admission_restriction_reason.is_empty()
                || uuid::Uuid::parse_str(&sync_data.admission_source_event_id).is_err()
            {
                return Err(ExecutorError::ExecutionFailed(
                    "STORAGE_CREATE_ADMISSION_INVALID".into(),
                ));
            }
            Some(AdmissionRecord {
                resource_id: uuid::Uuid::from_slice(&sync_data.bucket_id)
                    .map_err(|_| {
                        ExecutorError::ExecutionFailed("STORAGE_BUCKET_ID_INVALID".into())
                    })?
                    .to_string(),
                resource_name: sync_data.name.clone(),
                policy_version: sync_data.admission_policy_version,
                decision: sync_data.admission_decision.clone(),
                restriction_reason: None,
                effective_at_unix_seconds: effective_at,
                valid_until_unix_seconds: valid_until,
                source_event_id: sync_data.admission_source_event_id.clone(),
            })
        } else {
            None
        };

        // [COMMENT]: Validate credential fields — bắt buộc phải có đủ thông tin credential
        if sync_data.access_key.is_empty() || sync_data.secret_key.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "BucketCreateSync payload missing credential fields (access_key / secret_key)"
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
        let minio_client = MinioClient::from_env_private().await;
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
        // STEP 1.5: Thiết lập dung lượng quota cho bucket
        // ─────────────────────────────────────────────────────────────────────
        if sync_data.quota_bytes > 0 {
            Logger::sys_info(
                op,
                &format!(
                    "Step 1.5/3: Thiết lập quota {} bytes cho bucket '{}'...",
                    sync_data.quota_bytes, sync_data.name
                ),
            );
            if let Err(e) = admin_client
                .set_bucket_quota(&sync_data.name, sync_data.quota_bytes)
                .await
            {
                let msg = format!(
                    "Step 1.5/3 FAIL: Không thể thiết lập quota cho bucket '{}': {}",
                    sync_data.name, e
                );
                Logger::sys_error(op, &msg, "BUCKET_QUOTA_SET_FAILED");

                // Partial provisioning remains owned by this operation; retry forward.
                return Err(ExecutorError::Retryable(msg));
            }
            Logger::sys_info(
                op,
                &format!(
                    "Step 1.5/3 OK: Bucket '{}' quota được thiết lập.",
                    sync_data.name
                ),
            );
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

            return Err(ExecutorError::Retryable(msg));
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

            return Err(ExecutorError::Retryable(msg));
        }

        // [COMMENT]: Gắn policy vào user
        if let Err(e) = admin_client
            .attach_policy_to_user(&sync_data.access_key, &policy_name)
            .await
        {
            let msg = format!("Step 3/3 FAIL (attach_policy): {}", e);
            Logger::sys_error(op, &msg, "MINIO_POLICY_ATTACH_FAILED");

            return Err(ExecutorError::Retryable(msg));
        }

        Logger::sys_info(
            op,
            &format!(
                "Step 3/3 OK: Bucket policy gắn vào user '{}'. Provisioning hoàn tất.",
                sync_data.access_key
            ),
        );

        // Runtime registration and commercial admission are both required Zone
        // projections. A failure returns retryable after MinIO provisioning,
        // whose preceding operations are idempotent.
        if let Some(registration) = registration {
            registration.activate(self.zone_kv.clone()).await?;
        }
        if let Some(admission) = admission {
            apply_admission_to_kv(self.zone_kv.admission(), &admission).await?;
        }

        Ok(ExecutionResult {
            message: format!(
                "Bucket '{}' provisioned với access key '{}' (user + policy + bucket)",
                sync_data.name, sync_data.access_key
            ),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        })
    }
}
