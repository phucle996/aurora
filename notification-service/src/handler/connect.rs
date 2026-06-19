use crate::infra::grpc::GrpcAuthClient;
use crate::observability::logger::Logger;
use axum::{extract::State, http::StatusCode, response::IntoResponse, Json};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;

// Trạng thái chia sẻ cho Route Handler
#[derive(Clone)]
pub struct AppState {
    pub auth_client: GrpcAuthClient,
}

// Request gửi từ Centrifugo Connect Proxy chứa định danh client và request payload
#[derive(Deserialize)]
pub struct ConnectRequest {
    // ID định danh kết nối của Centrifugo Client
    pub client: String,
    // Thông tin chi tiết request (bao gồm headers/cookies)
    pub request: RequestInfo,
}

#[derive(Deserialize)]
pub struct RequestInfo {
    pub headers: HashMap<String, String>,
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
    Json(payload): Json<ConnectRequest>,
) -> impl IntoResponse {
    let start_time = std::time::Instant::now();

    // 1. Phân tích và trích xuất Trace Context từ headers Centrifugo gửi sang
    let traceparent_header = payload.request.headers.get("traceparent")
        .or_else(|| payload.request.headers.get("Traceparent"))
        .or_else(|| payload.request.headers.get("TRACEPARENT"))
        .map(|s| s.as_str())
        .unwrap_or("");
    
    let trace_ctx = crate::observability::otel::TraceContext::parse(traceparent_header)
        .unwrap_or_else(crate::observability::otel::TraceContext::new_random);

    // Thực thi toàn bộ quá trình xác thực trong phạm vi Trace Context
    let response = crate::observability::otel::CURRENT_TRACE.scope(trace_ctx, async move {
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
        _span.set_attribute(opentelemetry::KeyValue::new("client_id", payload.client.clone()));

        Logger::sys_info(
            "http.connect_attempt",
            &format!(
                "Received connect proxy request from Centrifugo client: {}",
                payload.client
            ),
        );

        // BƯỚC 1: Phân tích toàn bộ cookies thành key-value map để dễ truy vấn
        let cookie_header = payload
            .request
            .headers
            .get("cookie")
            .cloned()
            .unwrap_or_default();
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

            match state
                .auth_client
                .verify_admin_trinity_token(token, access_key, access_secret)
                .await
            {
                Ok(res) => {
                    let latency = start_time.elapsed().as_secs_f64() * 1000.0;
                    if res.valid {
                        Logger::sys_info(
                            "http.auth_success",
                            &format!("Admin authentication successful: {}", res.user_id),
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
                        "gRPC admin authentication call failed",
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

            match state
                .auth_client
                .verify_user_trinity_token(token, access_key, access_secret)
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
                        "gRPC user authentication call failed",
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
    }).await;

    // 2. Thu thập chỉ số hiệu năng HTTP sử dụng OTel Metrics
    let status_str = response.status().as_u16().to_string();
    crate::observability::metrics::MetricsManager::record_http_request(
        "/api/v1/realtime/connect",
        &status_str,
        start_time.elapsed(),
    );

    response
}
