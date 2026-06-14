use crate::observability::logger::Logger;
use prometheus::{register_int_counter_with_registry, Encoder, IntCounter, Registry, TextEncoder};
use std::sync::OnceLock;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

// Registry trung tâm để quản lý tất cả các metrics của job-proxy
static REGISTRY: OnceLock<Registry> = OnceLock::new();

// Các Counter giám sát trạng thái ứng dụng nguyên tử (Atomic counters)
static WAL_RECORDS_READ: OnceLock<IntCounter> = OnceLock::new();
static STREAM_JOBS_PUSHED: OnceLock<IntCounter> = OnceLock::new();
static RESULTS_CONSUMED: OnceLock<IntCounter> = OnceLock::new();
static NOTIFICATIONS_SENT: OnceLock<IntCounter> = OnceLock::new();

/// Trình quản lý chỉ số giám sát hiệu năng Prometheus cho Job-Proxy
pub struct MetricsManager;

impl MetricsManager {
    /// Khởi tạo Registry và đăng ký các metrics giám sát cốt lõi
    pub fn init() {
        // Khởi tạo registry duy nhất một lần (Thread-safe)
        let registry = REGISTRY.get_or_init(Registry::new);

        // 1. Đăng ký counter cho số dòng WAL nhận được từ Postgres Logical Replication
        let wal_counter = register_int_counter_with_registry!(
            "job_proxy_wal_records_read_total",
            "Tong so ban ghi WAL da doc tu PostgreSQL Logical Replication",
            registry
        )
        .expect("Không thể đăng ký metric job_proxy_wal_records_read_total");
        WAL_RECORDS_READ.set(wal_counter).unwrap();

        // 2. Đăng ký counter cho số jobs đã được push sang Redis Stream (jobs:zone_id)
        let jobs_pushed_counter = register_int_counter_with_registry!(
            "job_proxy_stream_jobs_pushed_total",
            "Tong so jobs da duoc day vao Redis Stream",
            registry
        )
        .expect("Không thể đăng ký metric job_proxy_stream_jobs_pushed_total");
        STREAM_JOBS_PUSHED.set(jobs_pushed_counter).unwrap();

        // 3. Đăng ký counter cho số kết quả thực thi job nhận về từ Dataplane (status=PROCESSING, SUCCEEDED, FAILED...)
        let results_consumed_counter = register_int_counter_with_registry!(
            "job_proxy_results_consumed_total",
            "Tong so ket qua thuc thi job da nhan tu Dataplane",
            registry
        )
        .expect("Không thể đăng ký metric job_proxy_results_consumed_total");
        RESULTS_CONSUMED.set(results_consumed_counter).unwrap();

        // 4. Đăng ký counter cho số lượng thông báo realtime đẩy sang Redis Stream (stream:job_notifications)
        let notifications_sent_counter = register_int_counter_with_registry!(
            "job_proxy_notifications_sent_total",
            "Tong so thong bao realtime da day vao stream:job_notifications",
            registry
        )
        .expect("Không thể đăng ký metric job_proxy_notifications_sent_total");
        NOTIFICATIONS_SENT.set(notifications_sent_counter).unwrap();

        Logger::sys_info(
            "metrics.init",
            "Observability Metrics: Prometheus metrics registry successfully initialized.",
        );

        // Khởi tạo một task chạy ngầm (Background Task) làm HTTP Server mini để expose cổng /metrics
        let registry_clone = registry.clone();
        tokio::spawn(async move {
            // Đọc port cấu hình từ biến môi trường (mặc định 9102)
            let port = std::env::var("METRICS_PORT").unwrap_or_else(|_| "9102".to_string());
            let addr = format!("0.0.0.0:{}", port);

            let listener = match TcpListener::bind(&addr).await {
                Ok(l) => l,
                Err(e) => {
                    Logger::sys_error(
                        "metrics.server",
                        "Không thể bind cổng metrics",
                        &e.to_string(),
                    );
                    return;
                }
            };
            Logger::sys_info(
                "metrics.server",
                &format!("Prometheus metrics server listening on http://{}", addr),
            );

            loop {
                // Chấp nhận các TCP connection inbound từ Prometheus scraper
                let (mut socket, _) = match listener.accept().await {
                    Ok(s) => s,
                    Err(_) => continue,
                };

                let registry_ref = registry_clone.clone();
                tokio::spawn(async move {
                    let mut buf = [0; 1024];
                    if let Ok(n) = socket.read(&mut buf).await {
                        if n > 0 {
                            let request_str = String::from_utf8_lossy(&buf[..n]);
                            // Chỉ xử lý các HTTP GET request truy cập endpoint /metrics
                            if request_str.starts_with("GET /metrics") {
                                let mut buffer = Vec::new();
                                let encoder = TextEncoder::new();
                                let metric_families = registry_ref.gather();

                                // Encode các metric sang định dạng văn bản Prometheus chuẩn (version=0.0.4)
                                if let Err(e) = encoder.encode(&metric_families, &mut buffer) {
                                    Logger::sys_error(
                                        "metrics.encode",
                                        "Lỗi mã hóa metrics",
                                        &e.to_string(),
                                    );
                                }

                                let body = String::from_utf8(buffer).unwrap_or_default();
                                let response = format!(
                                    "HTTP/1.1 200 OK\r\nContent-Type: text/plain; version=0.0.4\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                                    body.len(),
                                    body
                                );
                                let _ = socket.write_all(response.as_bytes()).await;
                                let _ = socket.flush().await;
                            } else {
                                // Trả về 404 cho bất kỳ request nào khác
                                let response = "HTTP/1.1 404 NOT FOUND\r\nContent-Length: 0\r\nConnection: close\r\n\r\n";
                                let _ = socket.write_all(response.as_bytes()).await;
                                let _ = socket.flush().await;
                            }
                        }
                    }
                });
            }
        });
    }

    /// Tăng số lượng WAL record đã đọc từ PostgreSQL (được gọi mỗi khi nhận được CDC event)
    pub fn inc_wal_records_read() {
        if let Some(counter) = WAL_RECORDS_READ.get() {
            counter.inc();
        }
    }

    /// Tăng số lượng stream job đã đẩy thành công sang Redis Stream (được gọi sau khi XADD job thành công)
    pub fn inc_stream_jobs_pushed() {
        if let Some(counter) = STREAM_JOBS_PUSHED.get() {
            counter.inc();
        }
    }

    /// Tăng số lượng kết quả job đã nhận từ Dataplane (được gọi sau khi đọc xong tin nhắn kết quả)
    pub fn inc_results_consumed() {
        if let Some(counter) = RESULTS_CONSUMED.get() {
            counter.inc();
        }
    }

    /// Tăng số lượng thông báo realtime đã gửi thành công (được gọi sau khi XADD notification thành công)
    pub fn inc_notifications_sent() {
        if let Some(counter) = NOTIFICATIONS_SENT.get() {
            counter.inc();
        }
    }
}
