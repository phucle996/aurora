use crate::app::state::AppState;
use crate::application::auth::ConnectAuthError;
use crate::contract::connect::{ConnectRequest, ConnectResponse, ConnectResult};
use crate::contract::realtime::{job_channel, runtime_channel};
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
use axum::{
    extract::{Json, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use opentelemetry::trace::{FutureExt, SpanKind};
use opentelemetry::KeyValue;
use serde_json::Value;
use std::sync::Arc;

pub async fn handle_connect(
    State(state): State<Arc<AppState>>,
    http_headers: HeaderMap,
    payload: Result<Json<ConnectRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let started = std::time::Instant::now();
    let payload = match payload {
        Ok(Json(payload)) if payload.is_valid() => payload,
        Ok(Json(_)) => {
            return finish_response(
                (StatusCode::UNPROCESSABLE_ENTITY, "invalid connect request").into_response(),
                started,
            );
        }
        Err(_) => {
            Logger::sys_warn(
                "http.connect_invalid",
                "Rejected malformed Centrifugo connect request",
                "CONNECT_REQUEST_INVALID",
            );
            return finish_response(
                (StatusCode::UNPROCESSABLE_ENTITY, "invalid connect request").into_response(),
                started,
            );
        }
    };

    let traceparent = payload.forwarded_header("traceparent").unwrap_or_default();
    let tracestate = payload.forwarded_header("tracestate").unwrap_or_default();
    let parent = OtelTracer::extract_context(traceparent, tracestate);
    let trace_context = OtelTracer::start_span_with_parent(
        "http.connect",
        SpanKind::Server,
        vec![
            KeyValue::new("http.request.method", "POST"),
            KeyValue::new("http.route", "/api/v1/realtime/connect"),
        ],
        &parent,
    );

    let response = async {
        let cookie_header = forwarded_cookie(&http_headers)
            .or_else(|| payload.forwarded_header("cookie"))
            .unwrap_or_default();
        let response = match state.authorizer.authorize(cookie_header).await {
            Ok(user_id) => {
                Logger::sys_info(
                    "http.connect_authorized",
                    "Centrifugo connect request authorized",
                );
                (
                    StatusCode::OK,
                    axum::Json(ConnectResponse {
                        result: ConnectResult {
                            user: user_id.clone(),
                            channels: vec![job_channel(&user_id), runtime_channel(&user_id)],
                        },
                    }),
                )
                    .into_response()
            }
            Err(error) => {
                let status = error.status_code();
                match error {
                    ConnectAuthError::MissingCredentials | ConnectAuthError::InvalidCredentials => {
                        Logger::sys_warn(
                            "http.connect_unauthorized",
                            "Centrifugo connect request rejected",
                            "CONNECT_AUTH_REJECTED",
                        );
                    }
                    ConnectAuthError::Unavailable => {
                        Logger::sys_error(
                            "http.connect_unavailable",
                            "Authentication dependency unavailable",
                            "CONNECT_AUTH_UNAVAILABLE",
                        );
                    }
                    ConnectAuthError::InvalidResponse => {
                        Logger::sys_error(
                            "http.connect_protocol_error",
                            "Authentication response failed validation",
                            "CONNECT_AUTH_RESPONSE_INVALID",
                        );
                    }
                }
                (status, "connect rejected").into_response()
            }
        };
        response
    }
    .with_context(trace_context.clone())
    .await;

    OtelTracer::finish_span(
        &trace_context,
        (response.status().is_server_error()).then_some("CONNECT_FAILED"),
    );
    finish_response(response, started)
}

fn forwarded_cookie(headers: &HeaderMap) -> Option<&str> {
    headers
        .get("cookie")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty())
}

fn finish_response(response: Response, started: std::time::Instant) -> Response {
    MetricsManager::record_http_request(
        "/api/v1/realtime/connect",
        &response.status().as_u16().to_string(),
        started.elapsed(),
    );
    Logger::access_log(
        "connect_proxy",
        "POST",
        "/api/v1/realtime/connect",
        i32::from(response.status().as_u16()),
        started.elapsed().as_secs_f64() * 1_000.0,
    );
    response
}

#[allow(dead_code)]
fn _redaction_guard(_: &Value) {}
