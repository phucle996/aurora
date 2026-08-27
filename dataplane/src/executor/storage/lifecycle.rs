use crate::executor::storage::core::MinioClient;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use aws_sdk_s3::types::{
    AbortIncompleteMultipartUpload, ExpirationStatus, LifecycleExpiration, LifecycleRule,
    LifecycleRuleFilter, NoncurrentVersionExpiration,
};
use prost::Message;
use std::sync::Arc;

use super::storage_proto;

/// Executor chịu trách nhiệm cập nhật S3 Lifecycle Configuration trên MinIO.
pub struct BucketLifecycleExecutor;

#[async_trait]
impl Executor for BucketLifecycleExecutor {
    async fn execute(&self, payload: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.bucket.lifecycle";

        let sync_data = match storage_proto::BucketLifecycleSync::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode BucketLifecycleSync payload: {}",
                    e
                )));
            }
        };

        if sync_data.name.is_empty() || sync_data.bucket_id != payload.resource_id {
            return Err(ExecutorError::ExecutionFailed(
                "BucketLifecycleSync payload missing required field: name".to_string(),
            ));
        }

        Logger::sys_info(
            op,
            &format!(
                "Configuring {} lifecycle rules for bucket '{}' (ID: {})",
                sync_data.rules.len(),
                sync_data.name,
                sync_data.bucket_id
            ),
        );

        let actual_rules = sync_data.rules.clone();
        let mut s3_rules = Vec::new();
        for r in sync_data.rules {
            let status = if r.enabled {
                ExpirationStatus::Enabled
            } else {
                ExpirationStatus::Disabled
            };

            let filter = LifecycleRuleFilter::builder().prefix(r.prefix).build();

            let mut builder = LifecycleRule::builder()
                .id(r.id)
                .status(status)
                .filter(filter);

            if r.expiration_days > 0 {
                builder = builder.expiration(
                    LifecycleExpiration::builder()
                        .days(r.expiration_days)
                        .build(),
                );
            }

            if r.noncurrent_version_expiration_days > 0 {
                builder = builder.noncurrent_version_expiration(
                    NoncurrentVersionExpiration::builder()
                        .noncurrent_days(r.noncurrent_version_expiration_days)
                        .build(),
                );
            }

            if r.abort_incomplete_multipart_upload_days > 0 {
                builder = builder.abort_incomplete_multipart_upload(
                    AbortIncompleteMultipartUpload::builder()
                        .days_after_initiation(r.abort_incomplete_multipart_upload_days)
                        .build(),
                );
            }

            s3_rules.push(builder.build().map_err(|e| {
                ExecutorError::ExecutionFailed(format!("Failed to build S3 lifecycle rule: {}", e))
            })?);
        }

        let s3_client = MinioClient::from_env_private().await;
        s3_client
            .put_bucket_lifecycle_configuration(&sync_data.name, s3_rules)
            .await
            .map_err(|e| {
                ExecutorError::ExecutionFailed(format!(
                    "Failed to put bucket lifecycle on MinIO for bucket '{}': {}",
                    sync_data.name, e
                ))
            })?;

        Logger::sys_info(
            op,
            &format!(
                "BucketLifecycleExecutor OK: Lifecycle rules updated for bucket '{}'.",
                sync_data.name
            ),
        );

        let result = storage_proto::BucketLifecycleAppliedV1 {
            schema_version: 1,
            bucket_id: sync_data.bucket_id,
            actual_rules,
        };
        Ok(ExecutionResult {
            message: format!("Bucket '{}' lifecycle rules configured", sync_data.name),
            result_payload: result.encode_to_vec(),
            result_payload_schema_version: 1,
        })
    }
}
