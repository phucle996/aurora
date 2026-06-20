use crate::executor::mail::registry::{mail_proto, EndpointMetadata, MailServerPool};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/mail/sync_endpoint.rs - BỘ ĐỒNG BỘ CẤU HÌNH ENDPOINT
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Tiếp nhận và xử lý hành động "create_endpoint" và "sync_endpoint".
///   - Giải mã Protobuf cấu hình SMTP vật lý.
///   - Đồng bộ cấu hình xuống bộ nhớ đệm L2 Redis (cô lập theo Zone).
///   - Phát tín hiệu Broadcast qua Redis Pub/Sub để thông báo tới các instance Dataplane khác.
///   - Cập nhật bộ nhớ đệm L1 RAM cục bộ (hủy actor cũ nếu có và ghi nhận metadata mới).
///

pub struct SmtpSyncExecutor {
    mail_server_pool: Arc<MailServerPool>,
    redis_mgr: Arc<RedisClientManager>,
    zone_id: String,
}

impl SmtpSyncExecutor {
    // Khởi tạo một đối tượng SmtpSyncExecutor mới
    pub fn new(
        mail_server_pool: Arc<MailServerPool>,
        redis_mgr: Arc<RedisClientManager>,
        zone_id: String,
    ) -> Self {
        Self {
            mail_server_pool,
            redis_mgr,
            zone_id,
        }
    }
}

#[async_trait]
impl Executor for SmtpSyncExecutor {
    // Thực thi đồng bộ cấu hình endpoint
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        // 1. Giải mã SmtpEndpointSync từ payload.payload (dữ liệu Protobuf nhị phân gửi từ Controlplane)
        let config = match mail_proto::SmtpEndpointSync::decode(payload.payload.as_slice()) {
            Ok(c) => c,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode SmtpEndpointSync Protobuf: {}",
                    e
                )));
            }
        };

        let endpoint_id = config.id.clone();

        // 2. Kết nối tới Redis Client của Zone hiện tại phục vụ việc ghi cache L2
        let redis_client = self.redis_mgr.client();
        let mut conn = match redis_client.get_multiplexed_async_connection().await {
            Ok(c) => c,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to connect to Redis Zone for L2 cache write: {}",
                    e
                )));
            }
        };

        // 3. Lưu trữ cấu hình Protobuf nhị phân thô vào L2 Redis Cache để Actor phục vụ Cold Start sau này có thể truy xuất
        let endpoint_key = format!("mail:zone:{}:endpoints:{}", self.zone_id, endpoint_id);
        let set_res: Result<(), redis::RedisError> = redis::cmd("SET")
            .arg(&endpoint_key)
            .arg(&payload.payload)
            .query_async(&mut conn)
            .await;

        if let Err(e) = set_res {
            return Err(ExecutorError::ExecutionFailed(format!(
                "Failed to write endpoint config to Redis L2 Cache: {}",
                e
            )));
        }

        // 4. Trích xuất thông tin metadata định tuyến để cập nhật vào Pool động
        let metadata = EndpointMetadata {
            weight: config.weight as u32,
            priority: config.priority as u32,
            max_connections: config.max_connections as u32,
            config_version: (config.updated_at & 0xFFFFFFFF) as u32, // Trích xuất 32-bit timestamp làm số hiệu phiên bản LWW (Last-Write-Wins)
        };

        // 5. Đồng bộ metadata vào Redis Hash `mail:zone:<zone_id>:server_pool` phục vụ tính toán định tuyến động
        let pool_key = format!("mail:zone:{}:server_pool", self.zone_id);
        let metadata_json = match serde_json::to_string(&metadata) {
            Ok(j) => j,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to serialize EndpointMetadata: {}",
                    e
                )));
            }
        };

        let hset_res: Result<(), redis::RedisError> = redis::cmd("HSET")
            .arg(&pool_key)
            .arg(&endpoint_id)
            .arg(&metadata_json)
            .query_async(&mut conn)
            .await;

        if let Err(e) = hset_res {
            return Err(ExecutorError::ExecutionFailed(format!(
                "Failed to write metadata to Redis server_pool: {}",
                e
            )));
        }

        // 6. Phát tín hiệu qua Redis Zone Pub/Sub để các node Dataplane HA khác biết và đồng bộ L1 cache của chúng
        let pubsub_channel = format!("mail:zone:{}:endpoint_events", self.zone_id);
        let event_payload = serde_json::json!({
            "event_type": "sync",
            "endpoint_id": endpoint_id,
            "metadata": metadata
        });

        let publish_res: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
            .arg(&pubsub_channel)
            .arg(event_payload.to_string())
            .query_async(&mut conn)
            .await;

        if let Err(e) = publish_res {
            Logger::sys_error(
                "executor.mail.sync",
                &format!("Failed to publish sync event to Redis: {}", e),
                "REDIS_PUBLISH_ERROR",
            );
        }

        // 7. Cập nhật trực tiếp lên L1 RAM Cache của tiến trình hiện tại.
        // Hủy actor cũ để ngắt các kết nối SMTP cũ, bắt buộc lần gửi thư kế tiếp phải chạy Cold Start cấu hình mới.
        self.mail_server_pool.remove_endpoint(&endpoint_id).await;
        self.mail_server_pool
            .update_metadata(endpoint_id.clone(), metadata)
            .await;

        Logger::sys_info(
            "executor.mail.sync",
            &format!(
                "Successfully processed sync for SMTP Endpoint {}: L2 cache written, PubSub broadcasted, L1 RAM reloaded.",
                endpoint_id
            ),
        );

        Ok(ExecutionResult {
            message: format!("Successfully sync mail endpoint {}", endpoint_id),
        })
    }
}
