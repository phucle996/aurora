use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::nats::NatsClient;
use crate::observability::logger::Logger;
use axum::{
    extract::State,
    http::{HeaderMap, StatusCode},
    response::IntoResponse,
    Json,
};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;

// Trạng thái chia sẻ cho Route Handler
#[derive(Clone)]
pub struct AppState {
    // [COMMENT]: NATS client trỏ đến kết nối NATS Core
    pub nats_client: NatsClient,
    pub _centrifugo_client: CentrifugoClient,
}

#[derive(Deserialize)]
pub struct ConnectRequest {
    // ID định danh kết nối của Centrifugo Client
    pub client: String,
    // Thông tin chi tiết request (bao gồm headers/cookies)
    pub request: Option<RequestInfo>,
}

#[derive(Deserialize)]
pub struct RequestInfo {
    pub headers: Option<HashMap<String, String>>,
}

// Response trả về cho Centrifugo
#[derive(Serialize)]
pub struct ConnectResponse {
    pub result: ConnectResult,
}

#[derive(Serialize)]
pub struct ConnectResult {
    pub user: String,
    pub channels: Vec<String>,
}

pub async fn handle_connect(
    State(state): State<Arc<AppState>>,
    // [COMMENT]: Nhận HTTP headers từ Centrifugo proxy POST request
    // Centrifugo forward Cookie qua header khi proxy_http_headers được config đúng
    http_headers: HeaderMap,
    Json(raw_payload): Json<serde_json::Value>,
) -> impl IntoResponse {
    let start_time = std::time::Instant::now();

    // Thử parse sang ConnectRequest struct để log chi tiết nếu lỗi
    let payload: ConnectRequest = match serde_json::from_value(raw_payload.clone()) {
        Ok(parsed) => parsed,
        Err(err) => {
            let raw_str = serde_json::to_string(&raw_payload).unwrap_or_default();
            Logger::sys_error(
                "http.connect_deserialize_failed",
                "Failed to deserialize ConnectRequest from Centrifugo",
                &format!("Error: {}, Raw payload: {}", err, raw_str),
            );
            let latency = start_time.elapsed().as_secs_f64() * 1000.0;
            Logger::access_log(
                "connect_proxy",
                "POST",
                "/api/v1/realtime/connect",
                422,
                latency,
            );
            return (
                StatusCode::UNPROCESSABLE_ENTITY,
                format!("Deserialization failed: {}", err),
            )
                .into_response();
        }
    };

    // 1. Phân tích và trích xuất Trace Context từ headers Centrifugo gửi sang
    let traceparent_header = payload
        .request
        .as_ref()
        .and_then(|r| r.headers.as_ref())
        .and_then(|h| {
            h.get("traceparent")
                .or_else(|| h.get("Traceparent"))
                .or_else(|| h.get("TRACEPARENT"))
        })
        .map(|s| s.as_str())
        .unwrap_or("");

    let trace_ctx = crate::observability::otel::TraceContext::parse(traceparent_header)
        .unwrap_or_else(crate::observability::otel::TraceContext::new_random);

    // Thực thi toàn bộ quá trình xác thực trong phạm vi Trace Context
    let response = crate::observability::otel::CURRENT_TRACE
        .scope(trace_ctx, async move {
            use opentelemetry::trace::{Span, Tracer};

            // Lấy Trace Context hiện tại từ task-local
            let trace_ctx_opt = crate::observability::otel::OtelTracer::get_current_trace();
            let tracer = opentelemetry::global::tracer("notification-service");

            let cx = if let Some(ref tc) = trace_ctx_opt {
                tc.get_otel_context()
            } else {
                opentelemetry::Context::current()
            };

            // Khởi tạo Span cho cuộc gọi Connect
            let mut _span = tracer.start_with_context("http.connect", &cx);
            _span.set_attribute(opentelemetry::KeyValue::new(
                "client_id",
                payload.client.clone(),
            ));

            Logger::sys_info(
                "http.connect_attempt",
                &format!(
                    "Received connect proxy request from Centrifugo client: {}",
                    payload.client
                ),
            );

            // BƯỚC 1: Ưu tiên đọc cookie từ HTTP header Centrifugo forward sang
            // Centrifugo forward Cookie qua proxy_http_headers → HTTP header của POST request
            let cookie_header = {
                let from_http = http_headers
                    .get("cookie")
                    .or_else(|| http_headers.get("Cookie"))
                    .and_then(|v| v.to_str().ok())
                    .unwrap_or("")
                    .to_string();

                if !from_http.is_empty() {
                    // Ưu tiên HTTP header (Centrifugo proxy forward)
                    from_http
                } else {
                    // Fallback: đọc từ JSON body (future Centrifugo versions)
                    payload
                        .request
                        .as_ref()
                        .and_then(|r| r.headers.as_ref())
                        .and_then(|h| {
                            h.get("cookie")
                                .or_else(|| h.get("Cookie"))
                                .or_else(|| h.get("COOKIE"))
                        })
                        .cloned()
                        .unwrap_or_default()
                }
            };
            let mut cookies = HashMap::new();
            for cookie in cookie_header.split(';') {
                let parts: Vec<&str> = cookie.split('=').map(|s| s.trim()).collect();
                if parts.len() == 2 {
                    cookies.insert(parts[0], parts[1]);
                }
            }

            // BƯỚC 2: Phân tách thành 2 luồng xử lý và gọi gRPC endpoint độc lập
            if cookies.contains_key("admin_api_token") {
                // Luồng 1: Xác thực Admin/SRE
                Logger::sys_info("http.auth_flow", "Processing Admin/SRE authentication flow");

                let token = cookies
                    .get("admin_api_token")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();
                let access_key = cookies
                    .get("access_key")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();
                let access_secret = cookies
                    .get("access_secret")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();

                if token.is_empty() || access_key.is_empty() || access_secret.is_empty() {
                    Logger::sys_warn(
                        "http.auth_validate",
                        "Missing required admin trinity token cookies",
                        "missing_cookies",
                    );
                    let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                    Logger::access_log(
                        "connect_proxy",
                        "POST",
                        "/api/v1/realtime/connect",
                        401,
                        latency,
                    );
                    return (StatusCode::UNAUTHORIZED, "Missing credentials").into_response();
                }

                match crate::service::auth::admin::verify_admin_token(
                    &state.nats_client.client(),
                    token,
                    access_key,
                    access_secret,
                )
                .await
                {
                    Ok(res) => {
                        let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                        if res.valid {
                            Logger::sys_info(
                                "http.auth_success",
                                &format!("Admin authentication successful: {}", res.admin_id),
                            );
                            Logger::access_log(
                                "connect_proxy",
                                "POST",
                                "/api/v1/realtime/connect",
                                200,
                                latency,
                            );

                            let response = ConnectResponse {
                                result: ConnectResult {
                                    user: res.admin_id.clone(),
                                    channels: vec![format!("personal:{}", res.admin_id)],
                                },
                            };
                            (StatusCode::OK, Json(response)).into_response()
                        } else {
                            Logger::sys_warn(
                                "http.auth_failed",
                                "Invalid admin trinity credentials",
                                "invalid_credentials",
                            );
                            Logger::access_log(
                                "connect_proxy",
                                "POST",
                                "/api/v1/realtime/connect",
                                401,
                                latency,
                            );
                            (StatusCode::UNAUTHORIZED, "Invalid credentials").into_response()
                        }
                    }
                    Err(status) => {
                        let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                        Logger::sys_error(
                            "http.auth_error",
                            "NATS admin authentication call failed",
                            &format!("{:?}", status),
                        );
                        Logger::access_log(
                            "connect_proxy",
                            "POST",
                            "/api/v1/realtime/connect",
                            500,
                            latency,
                        );
                        (
                            StatusCode::INTERNAL_SERVER_ERROR,
                            "Auth service unavailable",
                        )
                            .into_response()
                    }
                }
            } else if cookies.contains_key("access_token") {
                // Luồng 2: Xác thực End-User thông thường
                Logger::sys_info("http.auth_flow", "Processing End-User authentication flow");

                let token = cookies
                    .get("access_token")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();
                let access_key = cookies
                    .get("access_key")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();
                let access_secret = cookies
                    .get("access_secret")
                    .map(|&s| s.to_string())
                    .unwrap_or_default();

                if token.is_empty() || access_key.is_empty() || access_secret.is_empty() {
                    Logger::sys_warn(
                        "http.auth_validate",
                        "Missing required user trinity token cookies",
                        "missing_cookies",
                    );
                    let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                    Logger::access_log(
                        "connect_proxy",
                        "POST",
                        "/api/v1/realtime/connect",
                        401,
                        latency,
                    );
                    return (StatusCode::UNAUTHORIZED, "Missing credentials").into_response();
                }

                match crate::service::auth::user::verify_user_token(
                    &state.nats_client.client(),
                    token,
                    access_key,
                    access_secret,
                )
                .await
                {
                    Ok(res) => {
                        let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                        if res.valid {
                            Logger::sys_info(
                                "http.auth_success",
                                &format!("User authentication successful: {}", res.user_id),
                            );
                            Logger::access_log(
                                "connect_proxy",
                                "POST",
                                "/api/v1/realtime/connect",
                                200,
                                latency,
                            );

                            let response = ConnectResponse {
                                result: ConnectResult {
                                    user: res.user_id.clone(),
                                    channels: vec![format!("personal:{}", res.user_id)],
                                },
                            };
                            (StatusCode::OK, Json(response)).into_response()
                        } else {
                            Logger::sys_warn(
                                "http.auth_failed",
                                "Invalid user trinity credentials",
                                "invalid_credentials",
                            );
                            Logger::access_log(
                                "connect_proxy",
                                "POST",
                                "/api/v1/realtime/connect",
                                401,
                                latency,
                            );
                            (StatusCode::UNAUTHORIZED, "Invalid credentials").into_response()
                        }
                    }
                    Err(status) => {
                        let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                        Logger::sys_error(
                            "http.auth_error",
                            "NATS user authentication call failed",
                            &format!("{:?}", status),
                        );
                        Logger::access_log(
                            "connect_proxy",
                            "POST",
                            "/api/v1/realtime/connect",
                            500,
                            latency,
                        );
                        (
                            StatusCode::INTERNAL_SERVER_ERROR,
                            "Auth service unavailable",
                        )
                            .into_response()
                    }
                }
            } else {
                Logger::sys_warn(
                    "http.auth_validate",
                    "No trinity tokens found in cookies",
                    "no_tokens",
                );
                let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                Logger::access_log(
                    "connect_proxy",
                    "POST",
                    "/api/v1/realtime/connect",
                    401,
                    latency,
                );
                (StatusCode::UNAUTHORIZED, "Missing credentials").into_response()
            }
        })
        .await;

    // 2. Thu thập chỉ số hiệu năng HTTP sử dụng OTel Metrics
    let status_str = response.status().as_u16().to_string();
    crate::observability::metrics::MetricsManager::record_http_request(
        "/api/v1/realtime/connect",
        &status_str,
        start_time.elapsed(),
    );

    response
}
