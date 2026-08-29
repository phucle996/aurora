use crate::app::state::AppState;
use crate::observability::logger::Logger;
use crate::service::ports::notification_channel;
use crate::service::ConnectAuthError;
use axum::{
    extract::{Json, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;

// [COMMENT]: Giới hạn độ dài client_id tối đa cho Centrifugo webhook payload
pub const MAX_CLIENT_ID_BYTES: usize = 128;

// [COMMENT]: DTO request payload gửi từ Centrifugo Connect Webhook
#[derive(Debug, Deserialize)]
pub struct ConnectRequest {
    pub client: String,
    pub request: Option<RequestInfo>,
}

impl ConnectRequest {
    pub fn is_valid(&self) -> bool {
        !self.client.is_empty()
            && self.client.len() <= MAX_CLIENT_ID_BYTES
            && !self.client.chars().any(char::is_control)
    }

    pub fn forwarded_header(&self, name: &str) -> Option<&str> {
        self.request
            .as_ref()
            .and_then(|request| request.headers.as_ref())
            .and_then(|headers| {
                headers
                    .iter()
                    .find(|(key, _)| key.eq_ignore_ascii_case(name))
                    .map(|(_, value)| value.as_str())
            })
    }
}

#[derive(Debug, Deserialize)]
pub struct RequestInfo {
    pub headers: Option<HashMap<String, String>>,
}

// [COMMENT]: DTO response payload trả về cho Centrifugo Connect Webhook
#[derive(Debug, Serialize)]
pub struct ConnectResponse {
    pub result: ConnectResult,
}

#[derive(Debug, Serialize)]
pub struct ConnectResult {
    pub user: String,
    pub channels: Vec<String>,
}

// [COMMENT]: Handler tiếp nhận Centrifugo Connect Webhook (POST /api/v1/realtime/connect)
// Khi browser mở WebSocket wss://, Centrifugo gọi webhook này kèm cookies/headers để notification-service xác minh với ACR
pub async fn handle_connect(
    State(state): State<Arc<AppState>>,
    http_headers: HeaderMap,
    payload: Result<Json<ConnectRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let payload = match payload {
        Ok(Json(payload)) if payload.is_valid() => payload,
        Ok(Json(_)) => {
            return (StatusCode::UNPROCESSABLE_ENTITY, "invalid connect request").into_response();
        }
        Err(_) => {
            Logger::sys_warn(
                "http.connect_invalid",
                "Rejected malformed Centrifugo connect request",
                "CONNECT_REQUEST_INVALID",
            );
            return (StatusCode::UNPROCESSABLE_ENTITY, "invalid connect request").into_response();
        }
    };

    let cookie_header = forwarded_cookie(&http_headers)
        .or_else(|| payload.forwarded_header("cookie"))
        .unwrap_or_default();

    match state.authorizer.authorize(cookie_header).await {
        Ok(user_id) => {
            let channels = vec![notification_channel(&user_id)];
            (
                StatusCode::OK,
                Json(ConnectResponse {
                    result: ConnectResult {
                        user: user_id,
                        channels,
                    },
                }),
            )
                .into_response()
        }
        Err(err @ ConnectAuthError::MissingCredentials) => {
            Logger::sys_warn(
                "http.connect_unauthorized",
                "Rejected realtime connect attempt without valid authentication cookie",
                "MISSING_CREDENTIALS",
            );
            (err.status_code(), "authentication required").into_response()
        }
        Err(err @ ConnectAuthError::InvalidCredentials) => {
            Logger::sys_warn(
                "http.connect_unauthorized",
                "Rejected realtime connect attempt with invalid credentials",
                "INVALID_CREDENTIALS",
            );
            (err.status_code(), "invalid credentials").into_response()
        }
        Err(err @ ConnectAuthError::Unavailable) => {
            Logger::sys_error(
                "http.connect_dependency_unavailable",
                "Authentication backend unavailable for realtime connect",
                "AUTH_BACKEND_UNAVAILABLE",
            );
            (err.status_code(), "authentication service unavailable").into_response()
        }
        Err(err @ ConnectAuthError::InvalidResponse) => {
            Logger::sys_error(
                "http.connect_dependency_invalid",
                "Authentication backend returned invalid response for realtime connect",
                "AUTH_BACKEND_INVALID_RESPONSE",
            );
            (err.status_code(), "internal authentication error").into_response()
        }
    }
}

// [COMMENT]: Trích xuất cookie từ HTTP header "cookie"
fn forwarded_cookie(headers: &HeaderMap) -> Option<&str> {
    headers
        .get("cookie")
        .and_then(|value| value.to_str().ok())
        .filter(|cookie| !cookie.trim().is_empty())
}
