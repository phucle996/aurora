use crate::config::Config;
use crate::payload::JobExecutionResult;
use tokio_postgres::NoTls;

use futures_util::StreamExt;
use crate::logger::Logger;

/// ResultConsumer chịu trách nhiệm lắng nghe kết quả thực thi của Dataplane từ Redis Pub/Sub,
/// giải mã và thực hiện câu lệnh UPDATE cập nhật trạng thái job trực tiếp vào Postgres.
pub struct ResultConsumer {
    config: Config,
    redis_client: redis::Client,
}

impl ResultConsumer {
    /// Khởi tạo một ResultConsumer mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self { config, redis_client }
    }

    /// Khởi chạy vòng lặp nhận tin nhắn kết quả và cập nhật database Postgres
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info("result_consumer.run", "ResultConsumer: Bắt đầu kết nối tới PostgreSQL...");
        let (client, connection) = tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error("result_consumer.postgres", "ResultConsumer: Lỗi kết nối PostgreSQL", &e.to_string());
            }
        });

        // Đảm bảo search path nằm ở mail schema
        client.execute("SET search_path TO mail, public", &[]).await?;

        Logger::sys_info("result_consumer.run", "ResultConsumer: Kết nối tới Redis Pub/Sub...");
        let mut pubsub = self.redis_client.get_async_pubsub().await?;
        
        // Subscribe vào tất cả các kênh job_results:<job_id>
        pubsub.psubscribe("job_results:*").await?;

        Logger::sys_info("result_consumer.run", "ResultConsumer: Đang lắng nghe kết quả từ Redis Pub/Sub (pattern: job_results:*)...");

        let mut pubsub_stream = pubsub.on_message();

        while let Some(msg) = pubsub_stream.next().await {
            let payload: String = match msg.get_payload() {
                Ok(p) => p,
                Err(e) => {
                    Logger::sys_error("result_consumer.payload", "ResultConsumer: Lỗi lấy payload tin nhắn", &e.to_string());
                    continue;
                }
            };

            if let Err(err) = self.process_result(&payload, &client).await {
                Logger::sys_error("result_consumer.process", "ResultConsumer: Lỗi xử lý kết quả", &err.to_string());
            }
        }

        Ok(())
    }

    /// Cập nhật trạng thái Job vào Postgres dựa trên kết quả nhận từ Dataplane
    async fn process_result(
        &self,
        payload_str: &str,
        pg_client: &tokio_postgres::Client,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let result: JobExecutionResult = serde_json::from_str(payload_str)?;

        Logger::job_log(
            &result.job_id,
            "unknown",
            result.attempt,
            "result_consumer.recv",
            &format!("Nhận kết quả: status={}", result.result_status),
        );

        let status = result.result_status.clone();
        let error_code = result.error_code.clone();
        let error_message = if status == "SUCCEEDED" {
            None
        } else {
            Some(result.message.clone())
        };

        // Thực hiện cập nhật DB nguyên tử (Atomic Update) tránh xung đột trạng thái
        let rows_updated = if status == "SUCCEEDED" {
            pg_client.execute(
                "UPDATE mail_outbox_records 
                 SET status = $1, 
                     attempts = $2, 
                     last_attempt = CURRENT_TIMESTAMP, 
                     error_code = NULL, 
                     error_message = NULL
                 WHERE event_id = $3 AND status IN ('PENDING', 'PROCESSING', 'PUBLISHED')",
                &[&status, &(result.attempt as i32), &result.job_id],
            ).await?
        } else {
            pg_client.execute(
                "UPDATE mail_outbox_records 
                 SET status = $1, 
                     attempts = $2, 
                     last_attempt = CURRENT_TIMESTAMP, 
                     error_code = $3, 
                     error_message = $4
                 WHERE event_id = $5 AND status IN ('PENDING', 'PROCESSING', 'PUBLISHED')",
                &[&status, &(result.attempt as i32), &error_code, &error_message, &result.job_id],
            ).await?
        };

        if rows_updated > 0 {
            Logger::job_log(
                &result.job_id,
                "unknown",
                result.attempt,
                "result_consumer.update",
                &format!("Cập nhật thành công DB -> Trạng thái {}", status),
            );
        } else {
            Logger::job_log(
                &result.job_id,
                "unknown",
                result.attempt,
                "result_consumer.update_skip",
                "Không thể cập nhật Job (trạng thái hiện tại trong DB đã là SUCCEEDED/FAILED/CANCELLED hoặc không tìm thấy)",
            );
        }

        Ok(())
    }
}
