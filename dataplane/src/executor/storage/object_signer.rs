use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use serde::Serialize;
use std::time::Duration;

use super::storage_proto;
use crate::executor::storage::core::client::MinioClient;

// [COMMENT]: Định dạng item object trả về cho client duyệt
#[derive(Serialize)]
struct ObjectItem {
    key: String,
    size: i64,
    last_modified: String,
}

// [COMMENT]: Executor sinh Presigned URL thô cho Upload (PUT), Download (GET), hoặc Delete (DELETE) hoặc duyệt đối tượng (list)
pub struct ObjectPresignExecutor;

#[async_trait]
impl Executor for ObjectPresignExecutor {
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let op = "storage.object.presign";

        // 1. Giải mã protobuf ObjectPresignRequest
        let sync_data = match storage_proto::ObjectPresignRequest::decode(&payload.payload[..]) {
            Ok(data) => data,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode ObjectPresignRequest: {}",
                    e
                )));
            }
        };

        Logger::sys_info(
            op,
            &format!(
                "ObjectPresignExecutor: Bắt đầu xử lý hành động '{}' trong bucket '{}'",
                sync_data.action, sync_data.bucket_name
            ),
        );

        if sync_data.action == "list" {
            // --- Xử lý duyệt danh sách đối tượng (List Objects) ---
            // 2. Khởi tạo MinIO Client kết nối qua internal network (không cần public endpoint)
            let minio_client = MinioClient::from_env_private().await;
            let s3 = minio_client.s3();

            let mut objects_list = Vec::new();
            let mut continuation_token: Option<String> = None;

            // 3. Vòng lặp list objects đệ quy từ root
            loop {
                let mut req = s3.list_objects_v2().bucket(&sync_data.bucket_name);
                if let Some(ref token) = continuation_token {
                    req = req.continuation_token(token);
                }

                match req.send().await {
                    Ok(resp) => {
                        if let Some(contents) = resp.contents {
                            for obj in contents {
                                let key = obj.key.unwrap_or_default();
                                // Bỏ qua các key folder ảo rỗng do MinIO tạo ra để chỉ giữ lại file thực tế
                                if key.ends_with('/') && obj.size.unwrap_or(0) == 0 {
                                    continue;
                                }
                                objects_list.push(ObjectItem {
                                    key,
                                    size: obj.size.unwrap_or(0),
                                    last_modified: obj
                                        .last_modified
                                        .map(|dt| format!("{}", dt))
                                        .unwrap_or_default(),
                                });
                            }
                        }
                        if resp.is_truncated.unwrap_or(false) {
                            continuation_token = resp.next_continuation_token;
                        } else {
                            break;
                        }
                    }
                    Err(e) => {
                        return Err(ExecutorError::ExecutionFailed(format!(
                            "Lỗi khi quét danh sách objects từ MinIO: {}",
                            e
                        )));
                    }
                }
            }

            // 4. Serialize danh sách kết quả sang định dạng JSON thô gửi về Job Orchestrator
            let json_result = serde_json::to_string(&objects_list).map_err(|e| {
                ExecutorError::ExecutionFailed(format!(
                    "Failed to serialize objects list to JSON: {}",
                    e
                ))
            })?;

            Logger::sys_info(
                op,
                &format!(
                    "ObjectPresignExecutor List OK: Đã duyệt thành công {} objects trong bucket '{}'",
                    objects_list.len(),
                    sync_data.bucket_name
                ),
            );

            Ok(ExecutionResult {
                message: json_result,
            })
        } else {
            // --- Xử lý ký Presigned URL cho Upload, Download, Delete ---
            // 2. Khởi tạo MinioClient với Public Endpoint (Envoy-facing) để URL được ký trùng Host header với browser
            let minio_client = MinioClient::from_env_public().await;
            let s3_client = minio_client.s3();

            // 3. Thời gian hết hạn liên kết ngắn hạn (15 phút)
            let presigned_config = aws_sdk_s3::presigning::PresigningConfig::builder()
                .expires_in(Duration::from_secs(900))
                .build()
                .map_err(|e| {
                    ExecutorError::ExecutionFailed(format!(
                        "Failed to build presigning config: {}",
                        e
                    ))
                })?;

            // 4. Ký offline dựa trên Action yêu cầu và map kiểu trả về đồng nhất
            let url = match sync_data.action.as_str() {
                "upload" => {
                    let mut req = s3_client
                        .put_object()
                        .bucket(&sync_data.bucket_name)
                        .key(&sync_data.key);
                    if !sync_data.content_type.is_empty() {
                        req = req.content_type(&sync_data.content_type);
                    }
                    let presigned = req.presigned(presigned_config).await.map_err(|e| {
                        ExecutorError::ExecutionFailed(format!("Failed to sign upload: {}", e))
                    })?;
                    presigned.uri().to_string()
                }
                "download" => {
                    let presigned = s3_client
                        .get_object()
                        .bucket(&sync_data.bucket_name)
                        .key(&sync_data.key)
                        .presigned(presigned_config)
                        .await
                        .map_err(|e| {
                            ExecutorError::ExecutionFailed(format!(
                                "Failed to sign download: {}",
                                e
                            ))
                        })?;
                    presigned.uri().to_string()
                }
                "delete" => {
                    let presigned = s3_client
                        .delete_object()
                        .bucket(&sync_data.bucket_name)
                        .key(&sync_data.key)
                        .presigned(presigned_config)
                        .await
                        .map_err(|e| {
                            ExecutorError::ExecutionFailed(format!("Failed to sign delete: {}", e))
                        })?;
                    presigned.uri().to_string()
                }
                other => {
                    return Err(ExecutorError::ExecutionFailed(format!(
                        "Unsupported action for S3 object signing: {}",
                        other
                    )));
                }
            };

            Logger::sys_info(
                op,
                &format!(
                    "ObjectPresignExecutor URL OK: Đã ký thành công action '{}' cho key '{}'",
                    sync_data.action, sync_data.key
                ),
            );

            Ok(ExecutionResult { message: url })
        }
    }
}
