use lazy_static::lazy_static;
use prometheus::{
    register_counter_vec, register_histogram_vec, CounterVec, Encoder, HistogramVec, Opts,
    TextEncoder,
};
use std::net::SocketAddr;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

lazy_static! {
    // ------------------------------------------------------------------------
    // 1. HTTP Connect Proxy Metrics
    // ------------------------------------------------------------------------
    // Đếm tổng số lượng request xác thực kết nối nhận từ Centrifugo Connect Proxy
    pub static ref HTTP_REQUESTS_TOTAL: CounterVec = register_counter_vec!(
        Opts::new("notification_http_requests_total", "Tong so luong request connect proxy tu Centrifugo"),
        &["path", "status"]
    ).unwrap();

    // Đo độ trễ xử lý các request connect proxy (đơn vị: giây)
    pub static ref HTTP_REQUEST_DURATION_SECONDS: HistogramVec = register_histogram_vec!(
        "notification_http_request_duration_seconds",
        "Do tre xu ly connect proxy (seconds)",
        &["path", "status"],
        vec![0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0]
    ).unwrap();

    // ------------------------------------------------------------------------
    // 2. Outbound gRPC Authentication Metrics
    // ------------------------------------------------------------------------
    // Đếm số lượng cuộc gọi gRPC sang Controlplane để xác thực Token
    pub static ref GRPC_CALLS_TOTAL: CounterVec = register_counter_vec!(
        Opts::new("notification_grpc_calls_total", "Tong so luong cuoc goi gRPC sang Controlplane"),
        &["method", "status"]
    ).unwrap();

    // Đo độ trễ của các cuộc gọi gRPC sang Controlplane (đơn vị: giây)
    pub static ref GRPC_CALL_DURATION_SECONDS: HistogramVec = register_histogram_vec!(
        "notification_grpc_call_duration_seconds",
        "Do tre cuoc goi gRPC den Controlplane (seconds)",
        &["method", "status"],
        vec![0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5]
    ).unwrap();

    // ------------------------------------------------------------------------
    // 3. Redis Stream and Centrifugo Worker Metrics
    // ------------------------------------------------------------------------
    // Đếm số lượng sự kiện job lấy từ Redis Stream
    pub static ref REDIS_EVENTS_TOTAL: CounterVec = register_counter_vec!(
        Opts::new("notification_redis_events_total", "Tong so luong su kien tieu thu tu Redis Stream"),
        &["status"]
    ).unwrap();

    // Đếm số lượng thông báo đẩy thành công sang Centrifugo API
    pub static ref CENTRIFUGO_PUBLISHES_TOTAL: CounterVec = register_counter_vec!(
        Opts::new("notification_centrifugo_publishes_total", "Tong so tin nhan day sang Centrifugo API"),
        &["status"]
    ).unwrap();

    // Đo thời gian trễ từ khi event được tạo ở Postgres/Outbox đến khi đẩy tới client thành công
    pub static ref DELIVERED_EVENT_LAG_SECONDS: HistogramVec = register_histogram_vec!(
        "notification_delivered_event_lag_seconds",
        "Do tre tu luc tao job trong Postgres den luc Centrifugo publish (seconds)",
        &["status"],
        vec![0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0]
    ).unwrap();
}

pub struct PromRegistry;

impl PromRegistry {
    /// Khởi tạo Prometheus registry và mở cổng export metrics dựa trên cổng được cấu hình (ví dụ: port 2112)
    pub fn init(port: u16) {
        crate::observability::logger::Logger::sys_info(
            "metrics.init",
            &format!("Observability Prometheus: Dynamic metrics registry initialized. Exporting on port :{}...", port),
        );

        // Khởi tạo các biến tĩnh (Lazy static initialization)
        lazy_static::initialize(&HTTP_REQUESTS_TOTAL);
        lazy_static::initialize(&HTTP_REQUEST_DURATION_SECONDS);
        lazy_static::initialize(&GRPC_CALLS_TOTAL);
        lazy_static::initialize(&GRPC_CALL_DURATION_SECONDS);
        lazy_static::initialize(&REDIS_EVENTS_TOTAL);
        lazy_static::initialize(&CENTRIFUGO_PUBLISHES_TOTAL);
        lazy_static::initialize(&DELIVERED_EVENT_LAG_SECONDS);

        // Khởi chạy máy chủ HTTP siêu nhẹ phục vụ cho việc Scrape Metrics của Prometheus trên cổng cấu hình
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
                let (mut socket, _) = match listener.accept().await {
                    Ok(s) => s,
                    Err(_) => continue,
                };

                // Xử lý mỗi kết nối HTTP scrape trong một tokio task độc lập
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

                    // Tạo HTTP Response thô trả về cho Prometheus Scraper
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
