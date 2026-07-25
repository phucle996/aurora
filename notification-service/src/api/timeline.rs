use crate::app::state::AppState;
use crate::application::auth::ConnectAuthError;
use crate::observability::{logger::Logger, metrics::MetricsManager};
use crate::timeline::event::PageRequest;
use axum::{
    extract::{Json, Path, Query, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use chrono::{DateTime, Utc};
use serde::Deserialize;
use std::sync::Arc;
use uuid::Uuid;

#[derive(Debug, Deserialize)]
pub struct MarkReadRequest {
    created_at: DateTime<Utc>,
}

pub async fn list_activities(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Query(request): Query<PageRequest>,
) -> Response {
    let started = std::time::Instant::now();
    let response = match authorize(&state, &headers).await {
        Ok(user_id) => match state.activities.list(user_id, request).await {
            Ok(page) => Json(page).into_response(),
            Err(error) => timeline_error("activity.list", error),
        },
        Err(response) => response,
    };
    finish("GET", "/api/v1/me/activities", started, response)
}

pub async fn list_notifications(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Query(request): Query<PageRequest>,
) -> Response {
    let started = std::time::Instant::now();
    let response = match authorize(&state, &headers).await {
        Ok(user_id) => match state.inbox.list(user_id, request).await {
            Ok(page) => Json(page).into_response(),
            Err(error) => timeline_error("notification.list", error),
        },
        Err(response) => response,
    };
    finish("GET", "/api/v1/me/notifications", started, response)
}

pub async fn mark_notification_read(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Path(notification_id): Path<Uuid>,
    Json(request): Json<MarkReadRequest>,
) -> Response {
    let started = std::time::Instant::now();
    let response = match authorize(&state, &headers).await {
        Ok(user_id) => match state
            .inbox
            .mark_read(user_id, request.created_at, notification_id)
            .await
        {
            Ok(()) => StatusCode::NO_CONTENT.into_response(),
            Err(error) => timeline_error("notification.mark_read", error),
        },
        Err(response) => response,
    };
    finish(
        "PUT",
        "/api/v1/me/notifications/:id/read",
        started,
        response,
    )
}

pub async fn mark_all_notifications_read(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
) -> Response {
    let started = std::time::Instant::now();
    let response = match authorize(&state, &headers).await {
        Ok(user_id) => match state.inbox.mark_all_read(user_id).await {
            Ok(()) => StatusCode::NO_CONTENT.into_response(),
            Err(error) => timeline_error("notification.mark_all_read", error),
        },
        Err(response) => response,
    };
    finish(
        "PUT",
        "/api/v1/me/notifications/read-all",
        started,
        response,
    )
}

async fn authorize(state: &AppState, headers: &HeaderMap) -> Result<Uuid, Response> {
    let cookie = headers
        .get("cookie")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default();
    match state.authorizer.authorize(cookie).await {
        Ok(user_id) => {
            Uuid::parse_str(&user_id).map_err(|_| StatusCode::INTERNAL_SERVER_ERROR.into_response())
        }
        Err(error) => {
            let status = match error {
                ConnectAuthError::MissingCredentials | ConnectAuthError::InvalidCredentials => {
                    StatusCode::UNAUTHORIZED
                }
                ConnectAuthError::Unavailable => StatusCode::SERVICE_UNAVAILABLE,
                ConnectAuthError::InvalidResponse => StatusCode::INTERNAL_SERVER_ERROR,
            };
            Err((status, "request rejected").into_response())
        }
    }
}

fn timeline_error(operation: &'static str, error: crate::application::ports::AppError) -> Response {
    let invalid = error.downcast_ref::<std::io::Error>().is_some_and(|error| {
        matches!(
            error.kind(),
            std::io::ErrorKind::InvalidInput | std::io::ErrorKind::InvalidData
        )
    });
    if invalid {
        Logger::sys_warn(
            "http.timeline_invalid",
            "Rejected invalid timeline request",
            operation,
        );
        (StatusCode::BAD_REQUEST, "invalid timeline request").into_response()
    } else {
        Logger::sys_error(
            "http.timeline_failed",
            "Timeline dependency request failed",
            operation,
        );
        (
            StatusCode::SERVICE_UNAVAILABLE,
            "timeline temporarily unavailable",
        )
            .into_response()
    }
}

fn finish(
    method: &'static str,
    route: &'static str,
    started: std::time::Instant,
    response: Response,
) -> Response {
    MetricsManager::record_http_request(
        route,
        &response.status().as_u16().to_string(),
        started.elapsed(),
    );
    Logger::access_log(
        "timeline_api",
        method,
        route,
        i32::from(response.status().as_u16()),
        started.elapsed().as_secs_f64() * 1_000.0,
    );
    response
}
