use crate::config::Config;
use crate::payload::JobExecutionResult;
use tokio_postgres::NoTls;
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

    /// Khởi chạy vòng lặp nhận tin nhắn kết quả từ Redis Stream và cập nhật database Postgres
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

        Logger::sys_info("result_consumer.run", "ResultConsumer: Kết nối tới Redis...");
        // Khởi tạo một multiplexed connection để sử dụng cho các thao tác đọc ghi Redis Stream
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        let stream_key = &self.config.result_stream_name;
        let group_name = "job-proxy-group";
        
        // 1. Đảm bảo Consumer Group đã tồn tại cho stream kết quả (XGROUP CREATE stream_key group_name $ MKSTREAM)
        let _: redis::RedisResult<()> = redis::cmd("XGROUP")
            .arg("CREATE")
            .arg(stream_key)
            .arg(group_name)
            .arg("$")
            .arg("MKSTREAM")
            .query_async(&mut redis_conn)
            .await;

        let consumer_id = format!("job-proxy-{}", std::process::id());
        Logger::sys_info(
            "result_consumer.run",
            &format!("ResultConsumer: Đang lắng nghe kết quả từ Redis Stream: {} (Group: {}, Consumer: {})...", stream_key, group_name, consumer_id)
        );

        loop {
            // Đọc tin nhắn mới từ stream sử dụng cơ chế Consumer Group chặn (blocking read 2000ms)
            let reply: redis::Value = match redis::cmd("XREADGROUP")
                .arg("GROUP")
                .arg(group_name)
                .arg(&consumer_id)
                .arg("BLOCK")
                .arg(2000)
                .arg("COUNT")
                .arg(1)
                .arg("STREAMS")
                .arg(stream_key)
                .arg(">")
                .query_async(&mut redis_conn)
                .await 
            {
                Ok(val) => val,
                Err(e) => {
                    Logger::sys_error("result_consumer.read", "Lỗi đọc từ Redis Stream", &e.to_string());
                    tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                    continue;
                }
            };

            // Phân tích cú pháp tin nhắn Redis Stream
            // Định dạng trả về của XREADGROUP: [ [stream_key, [ [msg_id, [field, value, ...]], ... ]], ... ]
            if let redis::Value::Bulk(streams) = reply {
                if streams.is_empty() {
                    continue;
                }
                if let redis::Value::Bulk(ref stream_data) = streams[0] {
                    if stream_data.len() >= 2 {
                        if let redis::Value::Bulk(ref messages) = stream_data[1] {
                            for message in messages {
                                if let redis::Value::Bulk(ref msg_parts) = message {
                                    if msg_parts.len() >= 2 {
                                        let msg_id = match &msg_parts[0] {
                                            redis::Value::Data(bytes) => String::from_utf8_lossy(bytes).into_owned(),
                                            _ => continue,
                                        };

                                        if let redis::Value::Bulk(ref fields) = msg_parts[1] {
                                            let mut payload_str = None;
                                            for i in (0..fields.len()).step_by(2) {
                                                let key = match &fields[i] {
                                                    redis::Value::Data(bytes) => String::from_utf8_lossy(bytes).into_owned(),
                                                    _ => continue,
                                                };
                                                if key == "data" && i + 1 < fields.len() {
                                                    if let redis::Value::Data(bytes) = &fields[i + 1] {
                                                        payload_str = Some(String::from_utf8_lossy(bytes).into_owned());
                                                    }
                                                }
                                            }

                                            if let Some(payload) = payload_str {
                                                match self.process_result(&payload, &client, &mut redis_conn).await {
                                                    Ok(_) => {
                                                        // Acknowledge (XACK) tin nhắn sau khi xử lý và cập nhật DB thành công
                                                        let _: redis::RedisResult<i32> = redis::cmd("XACK")
                                                            .arg(stream_key)
                                                            .arg(group_name)
                                                            .arg(&msg_id)
                                                            .query_async(&mut redis_conn)
                                                            .await;
                                                    }
                                                    Err(err) => {
                                                        Logger::sys_error("result_consumer.process", "Lỗi xử lý kết quả", &err.to_string());
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
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
