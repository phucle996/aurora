use crate::observability::logger::Logger;
use crate::reverse_provider;
use super::notifier::JobNotifier;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

// [COMMENT]: L1 Dispatcher giải mã kết quả Protobuf từ Dataplane, định tuyến cập nhật DB qua phân hệ L2, và thực hiện push notify real-time qua NATS.
pub async fn dispatch_result(
    payload_bytes: &[u8],
    pg_client: &tokio_postgres::Client,
    nats_client: &async_nats::Client,
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

            // [COMMENT]: Định tuyến cập nhật DB outbox động sang phân hệ tương ứng (L1 routing)
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
                // [COMMENT]: Định tuyến xuống Storage L2 Dispatcher
                reverse_provider::storage::l2_dispatcher::dispatch_storage_result(
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
                        nats_client,
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
        }
        )
        .await
}
