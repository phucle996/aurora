use crate::config::Config;
use crate::payload::JobExecutionResult;
use tokio_postgres::NoTls;
use futures_util::StreamExt;
use crate::logger::Logger;
use prost::Message;

pub mod job_proto {
    // Nạp struct sinh tự động từ protobuf (job_event.proto)
    include!(concat!(env!("OUT_DIR"), "/job.rs"));
}

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

        // Khởi tạo một multiplexed connection để sử dụng cho lệnh XADD phát sự kiện notification
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

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

            if let Err(err) = self.process_result(&payload, &client, &mut redis_conn).await {
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
        redis_conn: &mut redis::aio::MultiplexedConnection,
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
        let error_message = if status == "SUCCEEDED" || status == "PROCESSING" {
            None
        } else {
            Some(result.message.clone())
        };

        // Thực hiện cập nhật DB nguyên tử (Atomic Update) tránh xung đột trạng thái
        // Lấy lại user_id, job_topic và trace_id bằng mệnh đề RETURNING để tạo sự kiện real-time
        let row_opt = if status == "SUCCEEDED" {
            pg_client.query_opt(
                "UPDATE mail_outbox_records 
                 SET status = $1, 
                     completed_at = CURRENT_TIMESTAMP, 
                     error_code = NULL, 
                     error_message = NULL
                 WHERE event_id = $2 AND status IN ('PENDING', 'PROCESSING', 'PUBLISHED')
                 RETURNING user_id, job_topic, trace_id",
                &[&status, &result.job_id],
            ).await?
        } else if status == "PROCESSING" {
            pg_client.query_opt(
                "UPDATE mail_outbox_records 
                 SET status = $1,
                     error_code = NULL, 
                     error_message = NULL
                 WHERE event_id = $2 AND status IN ('PENDING', 'PROCESSING', 'PUBLISHED')
                 RETURNING user_id, job_topic, trace_id",
                &[&status, &result.job_id],
            ).await?
        } else {
            pg_client.query_opt(
                "UPDATE mail_outbox_records 
                 SET status = $1, 
                     completed_at = CURRENT_TIMESTAMP, 
                     error_code = $2, 
                     error_message = $3
                 WHERE event_id = $4 AND status IN ('PENDING', 'PROCESSING', 'PUBLISHED')
                 RETURNING user_id, job_topic, trace_id",
                &[&status, &error_code, &error_message, &result.job_id],
            ).await?
        };

        if let Some(row) = row_opt {
            Logger::job_log(
                &result.job_id,
                "unknown",
                result.attempt,
                "result_consumer.update",
                &format!("Cập nhật thành công DB -> Trạng thái {}", status),
            );

            // Phân tích dữ liệu outbox record vừa được cập nhật
            let user_id: String = row.get(0);
            let job_topic: String = row.get(1);
            let trace_id: String = row.get::<_, Option<String>>(2).unwrap_or_default();

            // Lấy user_id trực tiếp từ cột DB. Nếu có, tiến hành phát sự kiện thông báo real-time.
            if !user_id.is_empty() {
                Logger::job_log(
                    &result.job_id,
                    &user_id,
                    result.attempt,
                    "result_consumer.notify_start",
                    &format!("Bắt đầu tạo sự kiện realtime cho user {}", user_id),
                );

                let notification_status = if status == "SUCCEEDED" {
                    "SUCCESS".to_string()
                } else if status == "PROCESSING" {
                    "PROCESSING".to_string()
                } else {
                    "FAILED".to_string()
                };

                // Đóng gói sự kiện JobNotificationEvent theo cấu trúc Protobuf
                let event = job_proto::JobNotificationEvent {
                    job_id: result.job_id.clone(),
                    user_id: user_id.clone(),
                    status: notification_status,
                    event_type: job_topic.clone(),
                    title: match job_topic.as_str() {
                        "mail.test_connection" => "SMTP Connection Test".to_string(),
                        _ => "Job Execution Result".to_string(),
                    },
                    message: result.message.clone(),
                    created_at: chrono::Utc::now().timestamp(),
                    trace_parent: trace_id,
                };

                // Mã hóa sự kiện sang dạng nhị phân Protobuf bytes
                let mut binary_buf = Vec::new();
                event.encode(&mut binary_buf)?;

                // Đẩy dữ liệu nhị phân vào Redis Stream bằng câu lệnh XADD
                let _: String = redis::cmd("XADD")
                    .arg("stream:job_notifications")
                    .arg("*")
                    .arg("data")
                    .arg(&binary_buf)
                    .query_async(redis_conn)
                    .await?;

                Logger::job_log(
                    &result.job_id,
                    &user_id,
                    result.attempt,
                    "result_consumer.notify_sent",
                    "Đã đẩy thành công sự kiện thông báo vào stream:job_notifications",
                );
            }
        } else {
            Logger::job_log(
                &result.job_id,
                "unknown",
                result.attempt,
                "result_consumer.update_skip",
                "Không thể cập nhật Job hoặc không tìm thấy bản ghi phù hợp (có thể đã hoàn thành trước đó)",
            );
        }

        Ok(())
    }
}
