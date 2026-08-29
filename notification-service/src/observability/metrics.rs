use opentelemetry::metrics::{Counter, Histogram};
use opentelemetry::{global, KeyValue};
use std::sync::OnceLock;
use std::time::Duration;

// [COMMENT]: Khai báo các đối tượng metric toàn cục OpenTelemetry với OnceLock khởi tạo lười
static HTTP_REQUESTS: OnceLock<Counter<u64>> = OnceLock::new();
static HTTP_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static REDIS_CALLS: OnceLock<Counter<u64>> = OnceLock::new();
static REDIS_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static REDIS_STREAM_EVENTS: OnceLock<Counter<u64>> = OnceLock::new();
static CENTRIFUGO_PUBLISHES: OnceLock<Counter<u64>> = OnceLock::new();
static EVENT_AGE_AT_PUBLISH: OnceLock<Histogram<f64>> = OnceLock::new();
static TELEMETRY_EVENTS: OnceLock<Counter<u64>> = OnceLock::new();

// [COMMENT]: MetricsManager quản lý việc ghi nhận các chỉ số hiệu năng và trạng thái của Notification Service
pub struct MetricsManager;

impl MetricsManager {
    // [COMMENT]: Khởi tạo trước toàn bộ metric để tránh độ trễ trong request path đầu tiên
    pub fn init() {
        let _ = Self::http_requests();
        let _ = Self::http_duration();
        let _ = Self::redis_calls();
        let _ = Self::redis_duration();
        let _ = Self::redis_stream_events();
        let _ = Self::centrifugo_publishes();
        let _ = Self::event_age_at_publish();
        let _ = Self::telemetry_events();
    }

    fn meter() -> opentelemetry::metrics::Meter {
        global::meter(super::SERVICE_NAME)
    }

    fn http_requests() -> &'static Counter<u64> {
        HTTP_REQUESTS.get_or_init(|| {
            Self::meter()
                .u64_counter("notification_http_requests_total")
                .with_description("Centrifugo connect proxy requests")
                .init()
        })
    }

    fn http_duration() -> &'static Histogram<f64> {
        HTTP_DURATION.get_or_init(|| {
            Self::meter()
                .f64_histogram("notification_http_request_duration_seconds")
                .with_description("Centrifugo connect proxy latency")
                .with_unit("s")
                .init()
        })
    }

    fn redis_calls() -> &'static Counter<u64> {
        REDIS_CALLS.get_or_init(|| {
            Self::meter()
                .u64_counter("notification_shared_redis_calls_total")
                .with_description("Central auth request-reply calls through Shared Redis")
                .init()
        })
    }

    fn redis_duration() -> &'static Histogram<f64> {
        REDIS_DURATION.get_or_init(|| {
            Self::meter()
                .f64_histogram("notification_shared_redis_call_duration_seconds")
                .with_description("Shared Redis auth request-reply latency")
                .with_unit("s")
                .init()
        })
    }

    fn redis_stream_events() -> &'static Counter<u64> {
        REDIS_STREAM_EVENTS.get_or_init(|| {
            Self::meter()
                .u64_counter("notification_shared_redis_stream_events_total")
                .with_description("Shared Redis durable stream outcomes")
                .init()
        })
    }

    fn centrifugo_publishes() -> &'static Counter<u64> {
        CENTRIFUGO_PUBLISHES.get_or_init(|| {
            Self::meter()
                .u64_counter("notification_centrifugo_publishes_total")
                .with_description("Centrifugo API publish outcomes")
                .init()
        })
    }

    fn event_age_at_publish() -> &'static Histogram<f64> {
        EVENT_AGE_AT_PUBLISH.get_or_init(|| {
            Self::meter()
                .f64_histogram("notification_event_age_at_centrifugo_publish_seconds")
                .with_description(
                    "Event age when Centrifugo acknowledges publish; not browser delivery latency",
                )
                .with_unit("s")
                .init()
        })
    }

    fn telemetry_events() -> &'static Counter<u64> {
        TELEMETRY_EVENTS.get_or_init(|| {
            Self::meter()
                .u64_counter("notification_telemetry_events_total")
                .with_description("Bounded telemetry pipeline attempts, drops, and suppression")
                .init()
        })
    }

    // [COMMENT]: Ghi nhận số lượng và thời gian thực thi của một HTTP request
    pub fn record_http_request(path: &str, status: &str, duration: Duration) {
        let attrs = [
            KeyValue::new("http.route", normalized_route(path)),
            KeyValue::new("http.response.status_class", status_class(status)),
        ];
        Self::http_requests().add(1, &attrs);
        Self::http_duration().record(duration.as_secs_f64(), &attrs);
    }

    // [COMMENT]: Ghi nhận kết quả và thời gian gọi RPC qua Shared Redis
    pub fn record_redis_call(channel: &str, status: &str, duration: Duration) {
        let attrs = [
            KeyValue::new("rpc.operation", redis_operation(channel)),
            KeyValue::new("outcome", generic_outcome(status)),
        ];
        Self::redis_calls().add(1, &attrs);
        Self::redis_duration().record(duration.as_secs_f64(), &attrs);
    }

    // [COMMENT]: Ghi nhận kết quả xử lý một sự kiện từ Redis Stream (durable delivery, invalid, DLQ)
    pub fn record_redis_stream_event(stream: &'static str, status: &'static str) {
        Self::redis_stream_events().add(
            1,
            &[
                KeyValue::new("stream.kind", stream_kind(stream)),
                KeyValue::new("outcome", stream_outcome(status)),
            ],
        );
    }

    // [COMMENT]: Ghi nhận kết quả publish thông báo sang Centrifugo HTTP API
    pub fn record_centrifugo_publish(status: &str) {
        Self::centrifugo_publishes().add(1, &[KeyValue::new("outcome", generic_outcome(status))]);
    }

    // [COMMENT]: Ghi nhận tình trạng bộ đệm log (thành công, bị rớt, bị nén tần suất)
    pub(crate) fn record_log_pipeline(attempted: u64, dropped: u64, suppressed: u64) {
        let counter = Self::telemetry_events();
        if attempted > 0 {
            counter.add(
                attempted,
                &[
                    KeyValue::new("signal", "log"),
                    KeyValue::new("outcome", "attempted"),
                ],
            );
        }
        if dropped > 0 {
            counter.add(
                dropped,
                &[
                    KeyValue::new("signal", "log"),
                    KeyValue::new("outcome", "dropped"),
                ],
            );
        }
        if suppressed > 0 {
            counter.add(
                suppressed,
                &[
                    KeyValue::new("signal", "log"),
                    KeyValue::new("outcome", "suppressed"),
                ],
            );
        }
    }
}

// [COMMENT]: Chuẩn hóa path của endpoint để tránh bùng nổ cardinality metric (chỉ chấp nhận các route hợp lệ)
fn normalized_route(path: &str) -> &'static str {
    match path {
        "/api/v1/realtime/connect" => "/api/v1/realtime/connect",
        "/api/v1/me/activity/list" => "/api/v1/me/activity/list",
        "/api/v1/me/notification/list" => "/api/v1/me/notification/list",
        "/api/v1/me/notification/:id/read" => "/api/v1/me/notification/:id/read",
        "/api/v1/me/notification/read-all" => "/api/v1/me/notification/read-all",
        _ => "other",
    }
}

// [COMMENT]: Nhóm status code theo lớp (2xx, 3xx, 4xx, 5xx)
fn status_class(status: &str) -> &'static str {
    match status.as_bytes().first() {
        Some(b'2') => "2xx",
        Some(b'3') => "3xx",
        Some(b'4') => "4xx",
        Some(b'5') => "5xx",
        _ => "other",
    }
}

// [COMMENT]: Chuẩn hóa tên kênh Redis RPC
fn redis_operation(channel: &str) -> &'static str {
    match channel {
        "verify_user_trinity_token" => "verify_user_trinity",
        "verify_admin_trinity_token" => "verify_admin_trinity",
        _ => "other",
    }
}

// [COMMENT]: Chuẩn hóa trạng thái kết quả của Redis Stream
fn stream_outcome(status: &str) -> &'static str {
    match status {
        "delivered" => "delivered",
        "delivery_failed" => "delivery_failed",
        "invalid_envelope" => "invalid_envelope",
        "invalid_contract" => "invalid_contract",
        _ => "other",
    }
}

// [COMMENT]: Chuẩn hóa loại Redis Stream
fn stream_kind(stream: &str) -> &'static str {
    match stream {
        "job_notification" => "job_notification",
        "user_activity" => "user_activity",
        _ => "other",
    }
}

// [COMMENT]: Chuẩn hóa trạng thái kết quả chung (success, timeout, failure, invalid)
fn generic_outcome(status: &str) -> &'static str {
    match status {
        "ok" | "success" | "delivered" => "success",
        "timeout" => "timeout",
        "failed" | "error" | "delivery_failed" => "failure",
        "invalid" | "invalid_contract" | "invalid_envelope" => "invalid",
        _ => "other",
    }
}

#[cfg(test)]
mod tests {
    use super::{generic_outcome, normalized_route, status_class};

    #[test]
    fn dynamic_metric_values_are_bounded() {
        assert_eq!(normalized_route("/tenant/attacker-controlled"), "other");
        assert_eq!(status_class("503"), "5xx");
        assert_eq!(generic_outcome("attacker-controlled"), "other");
    }
}
