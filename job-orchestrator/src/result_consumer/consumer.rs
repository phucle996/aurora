use super::notifier::JobNotifier;
use crate::config::Config;
use crate::observability::logger::Logger;
use tokio_postgres::NoTls;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

/// Trình tiêu thụ và cập nhật kết quả công việc chạy từ Dataplane về Controlplane
pub struct ResultConsumer {
    config: Config,
    redis_client: redis::Client,
}

impl ResultConsumer {
    /// Khởi tạo một ResultConsumer mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self {
            config,
            redis_client,
        }
    }

    /// Khởi chạy vòng lặp nhận tin nhắn kết quả từ Redis Stream và cập nhật database Postgres
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "result_consumer.run",
            "ResultConsumer: Bắt đầu kết nối tới PostgreSQL...",
        );
        let (client, connection) =
            tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error(
                    "result_consumer.postgres",
                    "ResultConsumer: Lỗi kết nối PostgreSQL",
                    &e.to_string(),
                );
            }
        });

        // Đảm bảo search path nằm ở các schema mail, iam và storage
        client
            .execute("SET search_path TO mail, iam, storage, public", &[])
            .await?;

        Logger::sys_info(
            "result_consumer.run",
            "ResultConsumer: Kết nối tới Redis...",
        );
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
                    Logger::sys_error(
                        "result_consumer.read",
                        "Lỗi đọc từ Redis Stream",
                        &e.to_string(),
                    );
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
                                            redis::Value::Data(bytes) => {
                                                String::from_utf8_lossy(bytes).into_owned()
                                            }
                                            _ => continue,
                                        };

                                        if let redis::Value::Bulk(ref fields) = msg_parts[1] {
                                            let mut payload_bytes = None;
                                            for i in (0..fields.len()).step_by(2) {
                                                let key = match &fields[i] {
                                                    redis::Value::Data(bytes) => {
                                                        String::from_utf8_lossy(bytes).into_owned()
                                                    }
                                                    _ => continue,
                                                };
                                                if key == "payload" && i + 1 < fields.len() {
                                                    if let redis::Value::Data(bytes) =
                                                        &fields[i + 1]
                                                    {
                                                        payload_bytes = Some(bytes.clone());
                                                    }
                                                }
                                            }

                                            if let Some(payload) = payload_bytes {
                                                match self
                                                    .process_result(
                                                        &payload,
                                                        &client,
                                                        &mut redis_conn,
                                                    )
                                                    .await
                                                {
                                                    Ok(_) => {
                                                        // Acknowledge (XACK) tin nhắn sau khi xử lý và cập nhật DB thành công
                                                        let _: redis::RedisResult<i32> =
                                                            redis::cmd("XACK")
                                                                .arg(stream_key)
                                                                .arg(group_name)
                                                                .arg(&msg_id)
                                                                .query_async(&mut redis_conn)
                                                                .await;
                                                    }
                                                    Err(err) => {
                                                        Logger::sys_error(
                                                            "result_consumer.process",
                                                            "Lỗi xử lý kết quả",
                                                            &err.to_string(),
                                                        );
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

    /// Cập nhật trạng thái Job vào Postgres dựa trên kết quả nhận từ Dataplane (Protobuf)
    async fn process_result(
        &self,
        payload_bytes: &[u8],
        pg_client: &tokio_postgres::Client,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        use prost::Message;

        let result = match job_proto::JobExecutionResultProto::decode(payload_bytes) {
            Ok(res) => res,
            Err(e) => {
                Logger::sys_error(
                    "result_consumer.decode",
                    "Không thể giải mã Protobuf payload từ Redis Stream",
                    &e.to_string(),
                );
                return Err(Box::new(e));
            }
        };

        // Convert UUID bytes từ Protobuf thành chuỗi string UUID
        let job_id = uuid::Uuid::from_slice(&result.job_id)
            .map(|u| u.to_string())
            .unwrap_or_default();

        // Convert trace_id bytes từ Protobuf thành chuỗi hex
        let _trace_id_from_proto = if result.trace_id.is_empty() {
            String::new()
        } else {
            result
                .trace_id
                .iter()
                .map(|b| format!("{:02x}", b))
                .collect::<String>()
        };

        // Tăng chỉ số metrics số kết quả nhận được từ Dataplane
        crate::observability::metrics::MetricsManager::inc_results_consumed();

        Logger::job_log(
            &job_id,
            &result.job_topic,
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

        // Thực hiện cập nhật DB nguyên tử (Atomic Update) tránh xung đột trạng thái.
        // Hỗ trợ môi trường phân tán HA: Chỉ cập nhật trạng thái khi bản ghi Outbox
        // đang ở trạng thái chưa hoàn tất (PENDING hoặc PROCESSING). Loại bỏ hoàn toàn PUBLISHED.
        // Lấy lại user_id, job_topic và trace_id bằng mệnh đề RETURNING để tạo sự kiện real-time.
        // Thêm kiểm tra topic khớp với database để tăng cường tính nhất quán bảo mật dữ liệu.
        // [COMMENT]: Loại bỏ ép kiểu ::uuid vì cột event_id trong DB là character varying(64).
        let table_name = if result.job_topic.starts_with("mail.") {
            "mail_outbox_records"
        } else if result.job_topic.starts_with("iam.") {
            "iam_outbox_records"
        } else if result.job_topic.starts_with("storage.") {
            "storage_outbox_records"
        } else {
            "mail_outbox_records"
        };

        let query_succeeded = format!(
            "UPDATE {} 
            SET status = $1, 
                completed_at = CURRENT_TIMESTAMP, 
                error_code = NULL, 
                error_message = NULL
            WHERE event_id = $2 AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING')
            RETURNING user_id, job_topic, trace_id, resource_id",
            table_name
        );

        let query_processing = format!(
            "UPDATE {} 
            SET status = $1,
                error_code = NULL, 
                error_message = NULL
            WHERE event_id = $2 AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING')
            RETURNING user_id, job_topic, trace_id, resource_id",
            table_name
        );

        let query_failed = format!(
            "UPDATE {} 
            SET status = $1, 
                completed_at = CURRENT_TIMESTAMP, 
                error_code = $2, 
                error_message = $3
            WHERE event_id = $4 AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING')
            RETURNING user_id, job_topic, trace_id, resource_id",
            table_name
        );

        let row_opt = if status == "SUCCEEDED" {
            pg_client
                .query_opt(&query_succeeded, &[&status, &job_id, &result.job_topic])
                .await?
        } else if status == "PROCESSING" {
            pg_client
                .query_opt(&query_processing, &[&status, &job_id, &result.job_topic])
                .await?
        } else {
            pg_client
                .query_opt(
                    &query_failed,
                    &[&status, &error_code, &error_message, &job_id, &result.job_topic],
                )
                .await?
        };

        if let Some(row) = row_opt {
            Logger::job_log(
                &job_id,
                &result.job_topic,
                result.attempt,
                "result_consumer.update",
                &format!("Cập nhật thành công DB -> Trạng thái {}", status),
            );

            // Phân tích dữ liệu outbox record vừa được cập nhật
            let user_id: String = row.get(0);
            let job_topic: String = row.get(1);
            // [COMMENT]: Đọc trace_id dưới dạng Option<Vec<u8>> vì cột trace_id đã được chuyển đổi sang kiểu BYTEA (nhị phân).
            // Sau đó chuyển đổi mảng byte này sang chuỗi hex 32 ký tự để khớp với định dạng OTel Trace ID.
            let trace_id_bytes = row.get::<_, Option<Vec<u8>>>(2).unwrap_or_default();
            let trace_id = if trace_id_bytes.is_empty() {
                String::new()
            } else {
                trace_id_bytes.iter().map(|b| format!("{:02x}", b)).collect::<String>()
            };
            let resource_id = row.get::<_, Option<String>>(3).unwrap_or_default();



            // Thiết lập scope trace_id và gửi thông báo real-time qua OTel span
            let trace_id_clone = trace_id.clone();
            let result_job_id = job_id.clone();
            let result_message = result.message.clone();
            let user_id_clone = user_id.clone();
            let status_clone = status.clone();
            let job_topic_clone = job_topic.clone();

            crate::observability::otel::CURRENT_TRACE_ID
                .scope(trace_id_clone, async move {
                    use opentelemetry::trace::{Span, TraceContextExt, Tracer};

                    // Phân tích trace context cha từ traceparent lưu trong DB
                    let cx = if let Some(parent_ctx) =
                        crate::observability::otel::OtelTracer::parse_traceparent(&trace_id)
                    {
                        opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                    } else {
                        opentelemetry::Context::current()
                    };

                    // Bắt đầu Span trong OTel/Tempo
                    let tracer = opentelemetry::global::tracer("job-proxy");
                    let mut span = tracer
                        .start_with_context(format!("result.notify.{}", job_topic_clone), &cx);

                    span.set_attribute(opentelemetry::KeyValue::new(
                        "job_id",
                        result_job_id.clone(),
                    ));
                    span.set_attribute(opentelemetry::KeyValue::new(
                        "user_id",
                        user_id_clone.clone(),
                    ));

                    // Lấy user_id trực tiếp từ cột DB. Nếu có, tiến hành phát sự kiện thông báo real-time.
                    if !user_id_clone.is_empty() {
                        let notify_res = JobNotifier::notify_realtime(
                            &result_job_id,
                            &user_id_clone,
                            result.attempt,
                            &status_clone,
                            &job_topic_clone,
                            &result_message,
                            &trace_id,
                            redis_conn,
                        )
                        .await;

                        if let Err(e) = notify_res {
                            span.record_error(e.as_ref());
                            return Err(e);
                        }
                    }

                    Ok(())
                })
                .await?;
        } else {
            Logger::job_log(
                &job_id,
                &result.job_topic,
                result.attempt,
                "result_consumer.update_skip",
                "Không thể cập nhật Job hoặc không tìm thấy bản ghi phù hợp (có thể đã hoàn thành trước đó hoặc lệch topic)",
            );
        }

        Ok(())
    }
}
