use crate::executor::storage::core::MinioClient;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;

use super::storage_proto;

/// [COMMENT]: Executor chịu trách nhiệm sinh STS Credentials tạm thời cho Bucket.
pub struct ObjectStsExecutor;

#[async_trait]
impl Executor for ObjectStsExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.object.sts";

        // [COMMENT]: 1. Giải mã (Decode) payload nhị phân sang struct ObjectStsRequest
        let sync_data = match storage_proto::ObjectStsRequest::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode ObjectStsRequest payload: {}",
                    e
                )));
            }
        };

        if sync_data.bucket_name.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "ObjectStsRequest payload missing required bucket name".to_string(),
            ));
        }

        let duration = if sync_data.duration_seconds > 0 {
            sync_data.duration_seconds
        } else {
            1800
        };

        Logger::sys_info(
            op,
            &format!(
                "Yêu cầu cấp STS token cho bucket '{}' với thời hạn {} giây...",
                sync_data.bucket_name, duration
            ),
        );

        // [COMMENT]: 2. Xây dựng Inline Policy JSON giới hạn quyền chỉ trong bucket và các object bên trong
        let policy = format!(
            r#"{{"Version":"2012-10-17","Statement":[{{"Effect":"Allow","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::{}"]}},{{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:GetObjectTagging","s3:PutObjectTagging"],"Resource":["arn:aws:s3:::{}/*"]}}]}}"#,
            sync_data.bucket_name, sync_data.bucket_name
        );

        // [COMMENT]: 3. Khởi tạo STS client và gọi AssumeRole
        let sts_client = MinioClient::sts_client_from_env().await;
        let sts_res = crate::observability::otel::OtelTracer::trace_result(
            "STS AssumeRole",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("rpc.system", "aws-api"),
                opentelemetry::KeyValue::new("rpc.service", "STS"),
                opentelemetry::KeyValue::new("rpc.method", "AssumeRole"),
            ],
            sts_client
                .assume_role()
                .role_arn("arn:aws:iam::123456789012:role/s3-console-direct")
                .role_session_name("s3-console-session")
                .policy(policy)
                .duration_seconds(duration as i32)
                .send(),
        )
        .await
        .map_err(|e| {
            ExecutorError::ExecutionFailed(format!("AssumeRole failed on MinIO: {}", e))
        })?;

        let credentials = sts_res.credentials.ok_or_else(|| {
            ExecutorError::ExecutionFailed("MinIO STS response missing credentials".to_string())
        })?;

        // [COMMENT]: 4. Chuyển đổi Expiration DateTime thành định dạng ISO 8601
        let expiration_iso = credentials.expiration.to_string();

        let config = crate::config::Config::get_global();
        let public_endpoint = config.minio_public_endpoint.clone();

        let response = storage_proto::ObjectStsResponse {
            access_key: credentials.access_key_id,
            secret_key: credentials.secret_access_key,
            session_token: credentials.session_token,
            expiration: expiration_iso.clone(),
            endpoint: public_endpoint,
        };

        let mut buf = Vec::new();
        if let Err(e) = response.encode(&mut buf) {
            return Err(ExecutorError::ExecutionFailed(format!(
                "Failed to serialize ObjectStsResponse: {}",
                e
            )));
        }

        let hex_encoded: String = buf.iter().map(|b| format!("{:02x}", b)).collect();

        Logger::sys_info(
            op,
            &format!(
                "Cấp STS token thành công cho bucket '{}'. Hạn dùng: {}",
                sync_data.bucket_name, expiration_iso
            ),
        );

        Ok(ExecutionResult {
            message: hex_encoded,
        })
    }
}
