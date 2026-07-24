use serde::{Deserialize, Serialize};

/// ============================================================================
/// 📂 MODULE: job-receiver/result.rs - Bộ Báo Cáo Kết Quả Xử Lý Nghiệp Vụ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng gói kết quả đầu ra sau khi Executor thực thi xong một Job nghiệp vụ.
///   - Báo cáo kết quả Protobuf qua Kafka để Job Orchestrator cập nhật outbox PostgreSQL.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái kết quả cuối cùng (Final outcome status) được quyết định bởi luồng thực thi của Executor.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kết quả trả về chỉ ghi nhận trạng thái kỹ thuật (Succeeded, Failed, Error Code, Return Message).
///   - TUYỆT ĐỐI KHÔNG chứa dữ liệu Tenant ID hay thông tin cá nhân khách hàng.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi Worker (`runner.rs`) và Watchdog (`watchdog.rs`) để đẩy kết quả vào Kafka.
///   - Job Orchestrator commit offset chỉ sau transaction cập nhật outbox hoàn tất.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Kafka dùng replication + manual commit; business side-effect vẫn phải idempotent theo job_id/version.
///
pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobExecutionResult {
    /// Mã định danh duy nhất của Job nghiệp vụ được xử lý.
    pub job_id: String,

    /// Phiên bản của Job (để so sánh tính nhất quán).
    pub job_version: u32,

    /// Số lần thử lại thực tế của Job này.
    pub attempt: u32,

    /// Trạng thái xử lý cuối cùng: "SUCCEEDED" | "FAILED" | "PROCESSING".
    pub result_status: String,

    /// Mã lỗi kỹ thuật phân loại cụ thể (nếu có). Ví dụ: "INSUFFICIENT_RESOURCE".
    pub error_code: Option<String>,

    /// Chuỗi thông báo mô tả chi tiết kết quả xử lý thực tế phục vụ gỡ lỗi (debugging).
    pub message: String,

    /// Tên topic sự kiện (e.g. "mail.test_connection")
    pub job_topic: String,

    /// Domain sở hữu outbox nguồn; được echo nguyên vẹn từ JobPayload.
    pub source_domain: String,

    /// OpenTelemetry trace id (32 ký tự hex)
    pub trace_id: String,
}

impl JobExecutionResult {
    /// Dịch kết quả thô của luồng thực thi (gồm cả Timeout) thành cấu trúc kết quả nghiệp vụ.
    pub fn from_outcome(
        job_id: String,
        job_version: u32,
        attempt: u32,
        job_topic: String,
        source_domain: String,
        trace_id: String,
        outcome: Result<
            Result<crate::executor::ExecutionResult, crate::executor::ExecutorError>,
            tokio::time::error::Elapsed,
        >,
    ) -> Self {
        match outcome {
            Ok(Ok(res)) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "SUCCEEDED".to_string(),
                error_code: None,
                message: res.message,
                job_topic,
                source_domain,
                trace_id,
            },
            Ok(Err(crate::executor::ExecutorError::ExecutionFailed(message))) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "FAILED".to_string(),
                error_code: Some("EXECUTION_FAILED".to_string()),
                message,
                job_topic,
                source_domain,
                trace_id,
            },
            Ok(Err(crate::executor::ExecutorError::Retryable(message))) => Self {
                job_id,
                job_version,
                attempt,
                // [COMMENT]: RETRYABLE chỉ điều khiển local PEL; không relay thành terminal outbox.
                result_status: "RETRYABLE".to_string(),
                error_code: Some("TRANSIENT_INFRASTRUCTURE".to_string()),
                message,
                job_topic,
                source_domain,
                trace_id,
            },
            Err(_) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "FAILED".to_string(),
                error_code: Some("EXECUTION_TIMEOUT".to_string()),
                message: "".to_string(),
                job_topic,
                source_domain,
                trace_id,
            },
        }
    }
}

// Helper giải mã chuỗi hex sang mảng byte nhị phân thô
fn decode_hex(s: &str) -> Vec<u8> {
    let mut bytes = Vec::new();
    let mut chars = s.chars();
    while let (Some(c1), Some(c2)) = (chars.next(), chars.next()) {
        if let Some(b) = hex_chars_to_byte(c1, c2) {
            bytes.push(b);
        }
    }
    bytes
}

fn hex_chars_to_byte(c1: char, c2: char) -> Option<u8> {
    let n1 = c1.to_digit(16)?;
    let n2 = c2.to_digit(16)?;
    Some((n1 << 4 | n2) as u8)
}

pub struct JobResultReporter;

impl JobResultReporter {
    /// [COMMENT]: Kafka producer chạy idempotent + acks=all; caller chỉ settle command sau khi hàm này thành công.
    pub async fn report_outcome(
        kafka: &crate::infra::kafka::KafkaTransport,
        result: &JobExecutionResult,
    ) -> Result<(), String> {
        use opentelemetry::trace::FutureExt;

        // Parse UUID string thành 16 bytes nhị phân
        let job_id_bytes = uuid::Uuid::parse_str(&result.job_id)
            .map(|value| value.as_bytes().to_vec())
            .map_err(|error| format!("result job_id is not a UUID: {error}"))?;

        // Convert hex trace_id thành 16 bytes nhị phân
        let trace_id_bytes = if result.trace_id.is_empty() {
            Vec::new()
        } else {
            decode_hex(&result.trace_id)
        };

        let result_topic = kafka.result_topic();
        let producer_context = crate::observability::otel::OtelTracer::start_current_span(
            format!("send {result_topic}"),
            opentelemetry::trace::SpanKind::Producer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "kafka"),
                opentelemetry::KeyValue::new("messaging.operation.type", "send"),
                opentelemetry::KeyValue::new("messaging.destination.name", result_topic.clone()),
                opentelemetry::KeyValue::new("aurora.job.id", result.job_id.clone()),
                opentelemetry::KeyValue::new("aurora.job.version", i64::from(result.job_version)),
                opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(result.attempt)),
                opentelemetry::KeyValue::new("aurora.job.outcome", result.result_status.clone()),
            ],
        );
        let propagation = crate::observability::otel::OtelTracer::inject_context(&producer_context);

        // [COMMENT]: Inject context của producer span, không reuse context của consumer
        // span; JO nhờ vậy nhìn thấy đúng cạnh Dataplane result-send → JO result-process.
        let proto_msg = job_proto::JobExecutionResultProto {
            job_id: job_id_bytes,
            job_version: result.job_version,
            attempt: result.attempt,
            result_status: result.result_status.clone(),
            job_topic: result.job_topic.clone(),
            trace_id: trace_id_bytes,
            error_code: result.error_code.clone(),
            message: result.message.clone(),
            source_domain: result.source_domain.clone(),
            traceparent: propagation.traceparent,
            tracestate: propagation.tracestate,
        };

        let key = proto_msg.job_id.clone();
        let publish_result = kafka
            .publish_message(&result_topic, &key, &proto_msg)
            .with_context(producer_context.clone())
            .await;
        crate::observability::otel::OtelTracer::finish_span(
            &producer_context,
            publish_result
                .as_ref()
                .err()
                .map(|_| "KAFKA_RESULT_PUBLISH_FAILED"),
        );
        publish_result?;

        crate::observability::logger::Logger::sys_info(
            "job.result",
            &format!(
                "Kafka result published for job {} [status={}]",
                result.job_id, result.result_status
            ),
        );

        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retryable_executor_error_never_becomes_terminal_failure() {
        let result = JobExecutionResult::from_outcome(
            uuid::Uuid::nil().to_string(),
            1,
            0,
            "mail.consumer.upsert".to_string(),
            "MAIL".to_string(),
            String::new(),
            Ok(Err(crate::executor::ExecutorError::Retryable(
                "redis unavailable".to_string(),
            ))),
        );
        assert_eq!(result.result_status, "RETRYABLE");
        assert_eq!(
            result.error_code.as_deref(),
            Some("TRANSIENT_INFRASTRUCTURE")
        );
    }
}
