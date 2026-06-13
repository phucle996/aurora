use std::net::SocketAddr;
use std::sync::OnceLock;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use prometheus::{
    register_counter_vec, register_gauge_vec, register_histogram_vec,
    CounterVec, Encoder, GaugeVec, HistogramVec, TextEncoder,
};

/// ============================================================================
/// 📂 MODULE: workerpool/metrics.rs - Quản Lý Chỉ Số Hiệu Năng Worker
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Cung cấp cơ cấu dữ liệu `MetricsType` đại diện cho các thông số vận hành của worker.
///   - Tích hợp Prometheus metrics để phục vụ giám sát trong môi trường Cloud Native & HA.
///   - Tự động expose endpoint `/metrics` thông qua TCP server siêu nhẹ để Prometheus pull dữ liệu.
///

// Sử dụng OnceLock (Standard Library) để khởi tạo các Prometheus Collector tĩnh một cách an toàn (lock-free, thread-safe).
static STREAM_LAG: OnceLock<GaugeVec> = OnceLock::new();
static EXEC_LATENCY: OnceLock<HistogramVec> = OnceLock::new();
static ACTIVE_CONNECTIONS: OnceLock<GaugeVec> = OnceLock::new();
static JOBS_PROCESSED: OnceLock<CounterVec> = OnceLock::new();

/// Khởi tạo hoặc lấy metric đo lường độ trễ (lag) của Redis Stream.
fn stream_lag() -> &'static GaugeVec {
    STREAM_LAG.get_or_init(|| {
        register_gauge_vec!(
            "dataplane_stream_lag",
            "Do lech (lag) doc tu Redis Stream cua tung Zone",
            &["zone_id"]
        ).unwrap()
    })
}

/// Khởi tạo hoặc lấy metric đo độ trễ xử lý các công việc nghiệp vụ.
fn exec_latency() -> &'static HistogramVec {
    EXEC_LATENCY.get_or_init(|| {
        register_histogram_vec!(
            "dataplane_job_execution_latency_seconds",
            "Do tre thuc thi cac job nghiep vu (seconds)",
            &["zone_id", "job_topic", "status"],
            vec![0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0]
        ).unwrap()
    })
}

/// Khởi tạo hoặc lấy metric đo số lượng active connections/workers.
fn active_connections() -> &'static GaugeVec {
    ACTIVE_CONNECTIONS.get_or_init(|| {
        register_gauge_vec!(
            "dataplane_active_connections_count",
            "So luong worker dang hoat dong dong thoi tai tung Zone",
            &["zone_id"]
        ).unwrap()
    })
}

/// Khởi tạo hoặc lấy metric đếm tổng số lượng job đã được xử lý (Succeeded/Failed).
fn jobs_processed() -> &'static CounterVec {
    JOBS_PROCESSED.get_or_init(|| {
        register_counter_vec!(
            "dataplane_jobs_processed_total",
            "Tong so luong job da duoc xu ly tai tung Zone",
            &["zone_id", "job_topic", "status"]
        ).unwrap()
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
    /// Khởi tạo các Prometheus metrics tĩnh khi hệ thống khởi động để đảm bảo sẵn sàng ghi nhận.
    pub fn init_registry() {
        // Force-initialize các collector tĩnh để đăng ký thành công vào Default Registry
        let _ = stream_lag();
        let _ = exec_latency();
        let _ = active_connections();
        let _ = jobs_processed();
    }

    /// Báo cáo và ghi nhận số liệu đo đạc dựa theo loại chỉ số được truyền vào.
    pub fn record_metrics(metrics: MetricsType) {
        match metrics {
            MetricsType::RedisStreamLag { zone_id, lag } => {
                stream_lag().with_label_values(&[&zone_id]).set(lag as f64);
            }
            MetricsType::HandlerLatencyMs {
                zone_id,
                job_topic,
                status,
                latency_ms,
            } => {
                // Đổi từ mili-giây sang giây để lưu trữ chuẩn hóa theo định dạng Prometheus (SI unit)
                let latency_sec = latency_ms / 1000.0;
                exec_latency()
                    .with_label_values(&[&zone_id, &job_topic, &status])
                    .observe(latency_sec);

                // Tăng bộ đếm tổng số lượng job đã xử lý
                jobs_processed()
                    .with_label_values(&[&zone_id, &job_topic, &status])
                    .inc();
            }
            MetricsType::ActiveConnectionsCount { zone_id, count } => {
                active_connections()
                    .with_label_values(&[&zone_id])
                    .set(count as f64);
            }
        }
    }
}

pub struct PromRegistry;

impl PromRegistry {
    /// Khởi chạy máy chủ HTTP siêu nhẹ phục vụ cho việc Scrape Metrics của Prometheus trên cổng cấu hình
    pub fn init(port: u16) {
        // Đăng ký các metrics trước khi expose endpoint
        WorkerMetricsManager::init_registry();

        crate::observability::logger::Logger::sys_info(
            "metrics.init",
            &format!("Observability Prometheus: Dynamic metrics registry initialized. Exporting on port :{}...", port),
        );

        // Khởi chạy tác vụ nền lắng nghe yêu cầu thu thập dữ liệu (scrape request) từ Prometheus
        tokio::spawn(async move {
            let addr = SocketAddr::from(([0, 0, 0, 0], port));
            let listener = match TcpListener::bind(addr).await {
                Ok(l) => l,
                Err(e) => {
                    crate::observability::logger::Logger::sys_error(
                        "metrics.server",
                        &format!(
                            "Failed to bind metrics HTTP server to 0.0.0.0:{}: {:?}",
                            port, e
                        ),
                        "bind_error",
                    );
                    return;
                }
            };

            loop {
                // Chấp nhận các kết nối TCP mới
                let (mut socket, _) = match listener.accept().await {
                    Ok(s) => s,
                    Err(_) => continue,
                };

                // Xử lý mỗi kết nối scrape trong một tokio task độc lập nhằm tối ưu hóa tính HA (High Availability)
                tokio::spawn(async move {
                    let mut buf = [0; 1024];
                    if socket.read(&mut buf).await.is_err() {
                        return;
                    }

                    // Tập hợp tất cả các chỉ số hiện có trong Default Registry
                    let encoder = TextEncoder::new();
                    let metric_families = prometheus::gather();
                    let mut buffer = Vec::new();

                    if encoder.encode(&metric_families, &mut buffer).is_err() {
                        return;
                    }

                    // Phản hồi HTTP 200 OK thô chứa metric payload theo định dạng text format v0.0.4 của Prometheus
                    let response = format!(
                        "HTTP/1.1 200 OK\r\nContent-Type: text/plain; version=0.0.4\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                        buffer.len()
                    );

                    let _ = socket.write_all(response.as_bytes()).await;
                    let _ = socket.write_all(&buffer).await;
                    let _ = socket.flush().await;
                });
            }
        });
    }
}
