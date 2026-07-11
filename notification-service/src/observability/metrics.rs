use opentelemetry::metrics::{Counter, Histogram};
use opentelemetry::{global, KeyValue};
use std::sync::OnceLock;
use std::time::Duration;

// ============================================================================
// 📂 MODULE: observability/metrics.rs - Native OTel Metrics for Notification
// ============================================================================
// Quản lý và khai báo các OTel Metric Instruments trực tiếp.
// Định dạng push-based truyền trực tiếp sang OTel Collector.
// ============================================================================

static HTTP_REQUESTS: OnceLock<Counter<u64>> = OnceLock::new();
static HTTP_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static NATS_CALLS: OnceLock<Counter<u64>> = OnceLock::new();
static NATS_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static NATS_EVENTS: OnceLock<Counter<u64>> = OnceLock::new();
static CENTRIFUGO_PUBLISHES: OnceLock<Counter<u64>> = OnceLock::new();
static DELIVERED_EVENT_LAG: OnceLock<Histogram<f64>> = OnceLock::new();

pub struct MetricsManager;

impl MetricsManager {
    /// Khởi tạo các OTel metrics tĩnh khi khởi động để sẵn sàng ghi nhận.
    pub fn init() {
        let _ = Self::http_requests();
        let _ = Self::http_duration();
        let _ = Self::nats_calls();
        let _ = Self::nats_duration();
        let _ = Self::nats_events();
        let _ = Self::centrifugo_publishes();
        let _ = Self::delivered_event_lag();
    }

    fn http_requests() -> &'static Counter<u64> {
        HTTP_REQUESTS.get_or_init(|| {
            global::meter("aurora-notification-service")
                .u64_counter("notification_http_requests_total")
                .with_description("Tong so luong request connect proxy tu Centrifugo")
                .init()
        })
    }

    fn http_duration() -> &'static Histogram<f64> {
        HTTP_DURATION.get_or_init(|| {
            global::meter("aurora-notification-service")
                .f64_histogram("notification_http_request_duration_seconds")
                .with_description("Do tre xu ly connect proxy (seconds)")
                .init()
        })
    }

    fn nats_calls() -> &'static Counter<u64> {
        NATS_CALLS.get_or_init(|| {
            global::meter("aurora-notification-service")
                .u64_counter("notification_nats_calls_total")
                .with_description("Tong so luong yeu cau request-reply qua NATS Core")
                .init()
        })
    }

    fn nats_duration() -> &'static Histogram<f64> {
        NATS_DURATION.get_or_init(|| {
            global::meter("aurora-notification-service")
                .f64_histogram("notification_nats_call_duration_seconds")
                .with_description("Do tre yeu cau request-reply qua NATS Core (seconds)")
                .init()
        })
    }

    fn nats_events() -> &'static Counter<u64> {
        NATS_EVENTS.get_or_init(|| {
            global::meter("aurora-notification-service")
                .u64_counter("notification_nats_events_total")
                .with_description("Tong so luong su kien tieu thu tu NATS Core")
                .init()
        })
    }

    fn centrifugo_publishes() -> &'static Counter<u64> {
        CENTRIFUGO_PUBLISHES.get_or_init(|| {
            global::meter("aurora-notification-service")
                .u64_counter("notification_centrifugo_publishes_total")
                .with_description("Tong so tin nhan day sang Centrifugo API")
                .init()
        })
    }

    fn delivered_event_lag() -> &'static Histogram<f64> {
        DELIVERED_EVENT_LAG.get_or_init(|| {
            global::meter("aurora-notification-service")
                .f64_histogram("notification_delivered_event_lag_seconds")
                .with_description("Do tre tu luc tao job den luc Centrifugo publish (seconds)")
                .init()
        })
    }

    /// Ghi nhận chỉ số request HTTP connect proxy
    pub fn record_http_request(path: &str, status: &str, duration: Duration) {
        let attrs = [
            KeyValue::new("path", path.to_string()),
            KeyValue::new("status", status.to_string()),
        ];
        Self::http_requests().add(1, &attrs);
        Self::http_duration().record(duration.as_secs_f64(), &attrs);
    }

    /// Ghi nhận chỉ số cuộc gọi request-reply tới NATS Core
    pub fn record_nats_call(subject: &str, status: &str, duration: Duration) {
        let attrs = [
            KeyValue::new("subject", subject.to_string()),
            KeyValue::new("status", status.to_string()),
        ];
        Self::nats_calls().add(1, &attrs);
        Self::nats_duration().record(duration.as_secs_f64(), &attrs);
    }

    /// Ghi nhận chỉ số sự kiện NATS Core
    pub fn record_nats_event(subject: &str, status: &str) {
        let attrs = [
            KeyValue::new("subject", subject.to_string()),
            KeyValue::new("status", status.to_string()),
        ];
        Self::nats_events().add(1, &attrs);
    }

    /// Ghi nhận chỉ số đẩy thông báo sang Centrifugo
    pub fn record_centrifugo_publish(status: &str) {
        Self::centrifugo_publishes().add(1, &[KeyValue::new("status", status.to_string())]);
    }

    /// Ghi nhận độ trễ xử lý thông báo E2E (Postgres -> WebSocket Client)
    pub fn record_delivered_lag(status: &str, duration: Duration) {
        let attrs = [KeyValue::new("status", status.to_string())];
        Self::delivered_event_lag().record(duration.as_secs_f64(), &attrs);
    }
}
