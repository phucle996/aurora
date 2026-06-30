use crate::observability::logger::Logger;
use opentelemetry::global;
use opentelemetry::metrics::{Counter, Gauge};
use std::sync::OnceLock;

// ============================================================================
// 📂 MODULE: observability/metrics.rs - OpenTelemetry Metrics for Job Proxy
// ============================================================================

// Khởi tạo các OTel Metric Instruments tĩnh một cách an toàn (Thread-safe)
static WAL_RECORDS_READ: OnceLock<Counter<u64>> = OnceLock::new();
static STREAM_JOBS_PUSHED: OnceLock<Counter<u64>> = OnceLock::new();
static RESULTS_CONSUMED: OnceLock<Counter<u64>> = OnceLock::new();
static NOTIFICATIONS_SENT: OnceLock<Counter<u64>> = OnceLock::new();
static QUEUE_LEN: OnceLock<Gauge<f64>> = OnceLock::new();
static PENDING_LEN: OnceLock<Gauge<f64>> = OnceLock::new();

/// Trình quản lý chỉ số giám sát OpenTelemetry cho Job Proxy
pub struct MetricsManager;

impl MetricsManager {
    /// Khởi tạo các OTel metrics tĩnh khi khởi động để sẵn sàng ghi nhận
    pub fn init() {
        let _ = Self::wal_records_read();
        let _ = Self::stream_jobs_pushed();
        let _ = Self::results_consumed();
        let _ = Self::notifications_sent();
        let _ = Self::queue_len_gauge();
        let _ = Self::pending_len_gauge();

        Logger::sys_info(
            "metrics.init",
            "Observability Metrics: OpenTelemetry metrics initialized successfully.",
        );
    }

    /// Khởi tạo hoặc lấy counter cho tổng số bản ghi WAL đã đọc
    fn wal_records_read() -> &'static Counter<u64> {
        WAL_RECORDS_READ.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .u64_counter("job_proxy_wal_records_read_total")
                .with_description("Tong so ban ghi WAL da doc tu PostgreSQL Logical Replication")
                .init()
        })
    }

    /// Khởi tạo hoặc lấy counter cho tổng số jobs đã được push vào Redis Stream
    fn stream_jobs_pushed() -> &'static Counter<u64> {
        STREAM_JOBS_PUSHED.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .u64_counter("job_proxy_stream_jobs_pushed_total")
                .with_description("Tong so jobs da duoc day vao Redis Stream")
                .init()
        })
    }

    /// Khởi tạo hoặc lấy counter cho tổng số kết quả thực thi job nhận về từ Dataplane
    fn results_consumed() -> &'static Counter<u64> {
        RESULTS_CONSUMED.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .u64_counter("job_proxy_results_consumed_total")
                .with_description("Tong so ket qua thuc thi job da nhan tu Dataplane")
                .init()
        })
    }

    /// Khởi tạo hoặc lấy counter cho tổng số thông báo realtime đã gửi đi
    fn notifications_sent() -> &'static Counter<u64> {
        NOTIFICATIONS_SENT.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .u64_counter("job_proxy_notifications_sent_total")
                .with_description("Tong so thong bao realtime da day vao stream:job_notifications")
                .init()
        })
    }

    /// Khởi tạo hoặc lấy gauge cho độ dài hàng đợi Redis Stream theo từng zone
    fn queue_len_gauge() -> &'static Gauge<f64> {
        QUEUE_LEN.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .f64_gauge("job_proxy_queue_len")
                .with_description("Do dai hien tai cua Redis Stream theo tung zone")
                .init()
        })
    }

    /// Khởi tạo hoặc lấy gauge cho số tin nhắn pending trong consumer group của từng zone
    fn pending_len_gauge() -> &'static Gauge<f64> {
        PENDING_LEN.get_or_init(|| {
            global::meter("aurora-job-orchestrator")
                .f64_gauge("job_proxy_pending_len")
                .with_description("So luong tin nhan pending trong consumer group cua cac zone")
                .init()
        })
    }

    /// Tăng số lượng WAL record đã đọc từ PostgreSQL (CDC event)
    pub fn inc_wal_records_read() {
        Self::wal_records_read().add(1, &[]);
    }

    /// Tăng số lượng stream job đã đẩy thành công sang Redis Stream
    pub fn inc_stream_jobs_pushed() {
        Self::stream_jobs_pushed().add(1, &[]);
    }

    /// Tăng số lượng kết quả job đã nhận từ Dataplane
    pub fn inc_results_consumed() {
        Self::results_consumed().add(1, &[]);
    }

    /// Tăng số lượng thông báo realtime đã gửi thành công
    pub fn inc_notifications_sent() {
        Self::notifications_sent().add(1, &[]);
    }
}
