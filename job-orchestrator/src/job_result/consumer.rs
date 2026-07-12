use super::notifier::JobNotifier;
use crate::config::Config;
use crate::observability::logger::Logger;
use crate::reverse_provider;
use tokio_postgres::NoTls;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

// [COMMENT]: JobResultConsumer tiêu thụ kết quả công việc từ Redis Stream, giải mã và định tuyến DB update.
pub struct JobResultConsumer {
    config: Config,
    redis_client: redis::Client,
    nats_client: async_nats::Client,
}

impl JobResultConsumer {
    // [COMMENT]: Khởi tạo một JobResultConsumer mới
    pub fn new(
        config: Config,
        redis_client: redis::Client,
        nats_client: async_nats::Client,
    ) -> Self {
        Self {
            config,
            redis_client,
            nats_client,
        }
    }

    // [COMMENT]: Khởi chạy luồng chặn đọc Redis Stream nhận kết quả từ Dataplane và update outbox (HA design)
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "job_result.run",
            "JobResultConsumer: Bắt đầu kết nối tới PostgreSQL...",
        );
        let (client, connection) =
            tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error(
                    "job_result.postgres",
                    "JobResultConsumer: Lỗi kết nối PostgreSQL",
                    &e.to_string(),
                );
            }
        });

        Logger::sys_info("job_result.run", "JobResultConsumer: Kết nối tới Redis...");
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        let stream_key = &self.config.result_stream_name;
        let group_name = "job-proxy-group";

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
            "job_result.run",
            &format!("JobResultConsumer: Đang lắng nghe kết quả từ Redis Stream: {} (Group: {}, Consumer: {})...", stream_key, group_name, consumer_id)
        );

        loop {
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
                    Logger::sys_error("job_result.read", "Lỗi đọc từ Redis Stream", &e.to_string());
                    tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                    continue;
                }
            };

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
                                                match self.process_result(&payload, &client).await {
                                                    Ok(_) => {
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
                                                            "job_result.process",
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

    // [COMMENT]: Giải mã kết quả Protobuf, cập nhật trạng thái outbox database tương ứng qua các reverse_provider
    async fn process_result(
        &self,
        payload_bytes: &[u8],
        pg_client: &tokio_postgres::Client,
    ) -> Result<(), Box<dyn std::error::Error>> {
        use prost::Message;

        let result = match job_proto::JobExecutionResultProto::decode(payload_bytes) {
            Ok(res) => res,
            Err(e) => {
                Logger::sys_error(
                    "job_result.decode",
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
        let trace_id_from_proto = if result.trace_id.is_empty() {
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

        let trace_id_clone = trace_id_from_proto.clone();

        // [COMMENT]: Bao bọc toàn bộ luồng logic log và DB update vào scope của trace_id nhận được từ Protobuf.
        // Điều này đảm bảo mọi dòng log in ra (recv, update, update_skip) đều chứa đúng trace_id.
        crate::observability::otel::CURRENT_TRACE_ID
            .scope(trace_id_clone, async move {
                Logger::job_log(
                    &job_id,
                    &result.job_topic,
                    result.attempt,
                    "job_result.recv",
                    &format!("Nhận kết quả: status={}", result.result_status),
                );

                let status = result.result_status.clone();
                let error_code = result.error_code.as_deref();
                let error_message = if status == "SUCCEEDED" || status == "PROCESSING" {
                    None
                } else {
                    Some(result.message.as_str())
                };

                let job_uuid = uuid::Uuid::from_slice(&result.job_id).unwrap_or_default();

                // [COMMENT]: Định tuyến cập nhật DB outbox động sang phân hệ tương ứng
                let row_opt = if result.job_topic.starts_with("mail.") {
                    reverse_provider::mail::db::update_outbox_record(
                        pg_client,
                        job_uuid,
                        &result.job_topic,
                        &status,
                        error_code,
                        error_message,
                    )
                    .await?
                } else if result.job_topic.starts_with("iam.") {
                    reverse_provider::iam::db::update_outbox_record(
                        pg_client,
                        job_uuid,
                        &result.job_topic,
                        &status,
                        error_code,
                        error_message,
                    )
                    .await?
                } else if result.job_topic.starts_with("storage.") {
                    reverse_provider::storage::db::update_outbox_record(
                        pg_client,
                        job_uuid,
                        &result.job_topic,
                        &status,
                        error_code,
                        error_message,
                    )
                    .await?
                } else {
                    // Fallback to mail outbox record if unknown
                    reverse_provider::mail::db::update_outbox_record(
                        pg_client,
                        job_uuid,
                        &result.job_topic,
                        &status,
                        error_code,
                        error_message,
                    )
                    .await?
                };

                if let Some(row) = row_opt {
                    Logger::job_log(
                        &job_id,
                        &result.job_topic,
                        result.attempt,
                        "job_result.update",
                        &format!("Cập nhật thành công DB -> Trạng thái {}", status),
                    );

                    let user_id: String = row.get(0);
                    let job_topic: String = row.get(1);
                    let trace_id_bytes = row.get::<_, Option<Vec<u8>>>(2).unwrap_or_default();
                    let trace_id = if trace_id_bytes.is_empty() {
                        String::new()
                    } else {
                        trace_id_bytes
                            .iter()
                            .map(|b| format!("{:02x}", b))
                            .collect::<String>()
                    };

                    let result_job_id = job_id.clone();
                    let result_message = result.message.clone();
                    let user_id_clone = user_id.clone();
                    let status_clone = status.clone();
                    let job_topic_clone = job_topic.clone();

                    use opentelemetry::trace::{Span, TraceContextExt, Tracer};

                    let cx = if let Some(parent_ctx) =
                        crate::observability::otel::OtelTracer::parse_traceparent(&trace_id)
                    {
                        opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                    } else {
                        opentelemetry::Context::current()
                    };

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

                    if !user_id_clone.is_empty() {
                        let notify_res = JobNotifier::notify_realtime(
                            &result_job_id,
                            &user_id_clone,
                            result.attempt,
                            &status_clone,
                            &job_topic_clone,
                            &result_message,
                            &trace_id,
                            &self.nats_client,
                        )
                        .await;

                        if let Err(e) = notify_res {
                            span.record_error(e.as_ref());
                            return Err(e);
                        }
                    }
                } else {
                    Logger::job_log(
                        &job_id,
                        &result.job_topic,
                        result.attempt,
                        "job_result.update_skip",
                        "Không thể cập nhật Job hoặc không tìm thấy bản ghi phù hợp (có thể đã hoàn thành trước đó hoặc lệch topic)",
                    );
                }

                Ok(())
            })
            .await
    }
}
