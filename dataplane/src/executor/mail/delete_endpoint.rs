use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use crate::executor::mail::registry::MailServerPool;
use crate::infra::redis::RedisClientManager;
use std::sync::Arc;
use async_trait::async_trait;

/// ============================================================================
/// 📂 MODULE: executor/mail/delete_endpoint.rs - BỘ XÓA ENDPOINT VẬT LÝ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Tiếp nhận và xử lý hành động "delete_endpoint".
///   - Xóa bỏ cấu hình SMTP vật lý khỏi Redis L2 Cache.
///   - Loại bỏ metadata định tuyến khỏi Redis Hash server_pool.
///   - Phát sự kiện "delete" qua Redis Zone Pub/Sub để đồng bộ các node HA khác.
///   - Hủy Endpoint khỏi L1 RAM Cache cục bộ để giải phóng actor và kết nối SMTP.
///

pub struct SmtpDeleteExecutor {
    mail_server_pool: Arc<MailServerPool>,
    redis_mgr: Arc<RedisClientManager>,
    zone_id: String,
}

impl SmtpDeleteExecutor {
    // Khởi tạo một đối tượng SmtpDeleteExecutor mới
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
impl Executor for SmtpDeleteExecutor {
    // Thực thi nghiệp vụ xóa endpoint
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        let endpoint_id = payload.resource_id.clone();
        
        // 1. Kiểm tra sự tồn tại của resource_id (định danh SMTP Endpoint cần xóa)
        if endpoint_id.is_empty() {
            return Err(ExecutorError::ExecutionFailed(
                "resource_id is required for delete_endpoint action".to_string(),
            ));
        }

        // 2. Kết nối tới Redis Client của Zone hiện tại
        let redis_client = self.redis_mgr.client();
        let mut conn = match redis_client.get_multiplexed_async_connection().await {
            Ok(c) => c,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to connect to Redis Zone: {}",
                    e
                )));
            }
        };

        let endpoint_key = format!("mail:zone:{}:endpoints:{}", self.zone_id, endpoint_id);
        let pool_key = format!("mail:zone:{}:server_pool", self.zone_id);

        // 3. Xóa bản ghi cấu hình nhị phân trong Redis L2 Cache
        let _: Result<(), _> = redis::cmd("DEL")
            .arg(&endpoint_key)
            .query_async(&mut conn)
            .await;

        // 4. Xóa metadata định tuyến trong Redis Hash server_pool
        let _: Result<(), _> = redis::cmd("HDEL")
            .arg(&pool_key)
            .arg(&endpoint_id)
            .query_async(&mut conn)
            .await;

        // 5. Broadcast sự kiện delete qua Redis Zone Pub/Sub để các node HA khác đồng bộ dọn dẹp L1 cache
        let pubsub_channel = format!("mail:zone:{}:endpoint_events", self.zone_id);
        let event_payload = serde_json::json!({
            "event_type": "delete",
            "endpoint_id": endpoint_id
        });

        let _: Result<(), _> = redis::cmd("PUBLISH")
            .arg(&pubsub_channel)
            .arg(event_payload.to_string())
            .query_async(&mut conn)
            .await;

        // 6. Loại bỏ Endpoint ra khỏi cache L1 RAM cục bộ (actor liên quan sẽ tự động được thu hồi và đóng kết nối SMTP)
        self.mail_server_pool.remove_endpoint(&endpoint_id).await;

        Logger::sys_info(
            "executor.mail.delete",
            &format!(
                "Successfully processed delete for SMTP Endpoint {}: L2 cache deleted, PubSub broadcasted, L1 RAM cleared.",
                endpoint_id
            ),
        );

        Ok(ExecutionResult {
            message: format!("Successfully deleted mail endpoint {}", endpoint_id),
        })
    }
}
