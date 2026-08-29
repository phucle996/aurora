use crate::observability::{logger::Logger, metrics::MetricsManager};
use axum::{
    extract::{MatchedPath, Request},
    middleware::Next,
    response::Response,
};

// [COMMENT]: Middleware tự động đo lường thời gian thực thi (Latency), ghi nhận Prometheus Metrics và xuất Structured Access Log cho toàn bộ HTTP routes
pub async fn http_telemetry_middleware(request: Request, next: Next) -> Response {
    let started = std::time::Instant::now();
    let method = request.method().as_str().to_string();
    // [COMMENT]: Trích xuất MatchedPath từ Axum router để tránh bùng nổ cardinality metric do các tham số URL động (:id)
    let route = request
        .extensions()
        .get::<MatchedPath>()
        .map(|p| p.as_str().to_string())
        .unwrap_or_else(|| request.uri().path().to_string());

    // [COMMENT]: Chuyển tiếp request đến handler tiếp theo trong pipeline
    let response = next.run(request).await;

    let elapsed = started.elapsed();
    let status = response.status().as_u16();

    // [COMMENT]: 1. Ghi nhận Prometheus Metrics độ trễ và mã HTTP status
    MetricsManager::record_http_request(&route, &status.to_string(), elapsed);

    // [COMMENT]: 2. Ghi Access Log có cấu trúc phục vụ kiểm toán và giám sát
    Logger::access_log(
        "timeline_api",
        &method,
        &route,
        i32::from(status),
        elapsed.as_secs_f64() * 1_000.0,
    );

    response
}
