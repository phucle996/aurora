use std::sync::OnceLock;
use opentelemetry::{global, KeyValue};
use opentelemetry::metrics::{Counter, Gauge, Histogram};

/// ============================================================================
/// 📂 MODULE: workerpool/metrics.rs - Quản Lý Chỉ Số Hiệu Năng Worker (OTel Push)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Cung cấp cơ cấu dữ liệu `MetricsType` đại diện cho các thông số vận hành của worker.
///   - Tích hợp OpenTelemetry Metrics để tự động đẩy dữ liệu lên OTel Collector định kỳ.
///   - Loại bỏ hoàn toàn HTTP scraper port cũ (Port 2112) phục vụ mô hình bảo mật zero-inbound.
///

// Sử dụng OnceLock để khởi tạo các OTel Metric Instruments tĩnh một cách an toàn.
static STREAM_LAG: OnceLock<Gauge<f64>> = OnceLock::new();
static EXEC_LATENCY: OnceLock<Histogram<f64>> = OnceLock::new();
static ACTIVE_CONNECTIONS: OnceLock<Gauge<f64>> = OnceLock::new();
static JOBS_PROCESSED: OnceLock<Counter<u64>> = OnceLock::new();

/// Khởi tạo hoặc lấy metric đo lường độ trễ (lag) của Redis Stream.
fn stream_lag() -> &'static Gauge<f64> {
    STREAM_LAG.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_stream_lag")
            .with_description("Do lech (lag) doc tu Redis Stream cua tung Zone")
            .init()
    })
}

/// Khởi tạo hoặc lấy metric đo độ trễ xử lý các công việc nghiệp vụ.
fn exec_latency() -> &'static Histogram<f64> {
    EXEC_LATENCY.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_histogram("dataplane_job_execution_latency_seconds")
            .with_description("Do tre thuc thi cac job nghiep vu (seconds)")
            .init()
    })
}

/// Khởi tạo hoặc lấy metric đo số lượng active connections/workers.
fn active_connections() -> &'static Gauge<f64> {
    ACTIVE_CONNECTIONS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_active_connections_count")
            .with_description("So luong worker dang hoat dong dong thoi tai tung Zone")
            .init()
    })
}

/// Khởi tạo hoặc lấy metric đếm tổng số lượng job đã được xử lý (Succeeded/Failed).
fn jobs_processed() -> &'static Counter<u64> {
    JOBS_PROCESSED.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("dataplane_jobs_processed_total")
            .with_description("Tong so luong job da duoc xu ly tai tung Zone")
            .init()
    })
}

#[derive(Clone, Debug)]
pub enum MetricsType {
    /// Độ lệch (lag) giữa vị trí đọc hiện tại và đầu cuối của Redis Stream.
    RedisStreamLag { zone_id: String, lag: u64 },

    /// Thời gian xử lý thực tế của một Job nghiệp vụ (tính bằng mili-giây).
    HandlerLatencyMs {
        zone_id: String,
        job_topic: String,
        status: String,
        latency_ms: f64,
    },

    /// Số lượng kết nối mạng/luồng xử lý đang hoạt động đồng thời.
    ActiveConnectionsCount { zone_id: String, count: usize },
}

pub struct WorkerMetricsManager;

impl WorkerMetricsManager {
    /// Khởi tạo các OTel metrics tĩnh khi hệ thống khởi động để đảm bảo sẵn sàng ghi nhận.
    pub fn init_registry() {
        let _ = stream_lag();
        let _ = exec_latency();
        let _ = active_connections();
        let _ = jobs_processed();
    }

    /// Báo cáo và ghi nhận số liệu đo đạc dựa theo loại chỉ số được truyền vào.
    pub fn record_metrics(metrics: MetricsType) {
        match metrics {
            MetricsType::RedisStreamLag { zone_id, lag } => {
                stream_lag().record(lag as f64, &[KeyValue::new("zone_id", zone_id)]);
            }
            MetricsType::HandlerLatencyMs {
                zone_id,
                job_topic,
                status,
                latency_ms,
            } => {
                // Đổi từ mili-giây sang giây để lưu trữ chuẩn hóa theo định dạng OTel
                let latency_sec = latency_ms / 1000.0;
                exec_latency().record(
                    latency_sec,
                    &[
                        KeyValue::new("zone_id", zone_id.clone()),
                        KeyValue::new("job_topic", job_topic.clone()),
                        KeyValue::new("status", status.clone()),
                    ],
                );

                // Tăng bộ đếm tổng số lượng job đã xử lý
                jobs_processed().add(
                    1,
                    &[
                        KeyValue::new("zone_id", zone_id),
                        KeyValue::new("job_topic", job_topic),
                        KeyValue::new("status", status),
                    ],
                );
            }
            MetricsType::ActiveConnectionsCount { zone_id, count } => {
                active_connections().record(count as f64, &[KeyValue::new("zone_id", zone_id)]);
            }
        }
    }
}
