use crate::app::state::AppState;
use crate::middleware::AuthUser;
use crate::observability::logger::Logger;
use crate::repo::PageRequest;
use axum::{
    extract::{Extension, Json, Path, Query, State},
    http::StatusCode,
    response::{IntoResponse, Response},
};
use chrono::{DateTime, Utc};
use serde::Deserialize;
use std::sync::Arc;
use uuid::Uuid;

// [COMMENT]: DTO Payload yêu cầu đánh dấu một thông báo đã đọc
// Chứa timestamp created_at (UTC) đóng vai trò là một phần của ScyllaDB clustering/partition key để định vị chính xác bản ghi
#[derive(Debug, Deserialize)]
pub struct MarkReadRequest {
    pub created_at: DateTime<Utc>,
}

// [COMMENT]: HTTP GET /api/v1/me/activity/list
// Lấy danh sách dòng sự kiện hoạt động (Activity Timeline) phân trang của người dùng hiện tại
pub async fn list_activities(
    State(state): State<Arc<AppState>>,
    Extension(AuthUser(user_id)): Extension<AuthUser>,
    Query(request): Query<PageRequest>,
) -> Response {
    match state.activities.list(user_id, request).await {
        Ok(page) => Json(page).into_response(),
        Err(error) => {
            let is_invalid = error.downcast_ref::<std::io::Error>().is_some_and(|e| {
                matches!(
                    e.kind(),
                    std::io::ErrorKind::InvalidInput | std::io::ErrorKind::InvalidData
                )
            });
            if is_invalid {
                Logger::sys_warn(
                    "http.timeline_invalid",
                    "Rejected invalid timeline request",
                    "activity.list",
                );
                (StatusCode::BAD_REQUEST, "invalid timeline request").into_response()
            } else {
                Logger::sys_error(
                    "http.timeline_failed",
                    "Timeline dependency request failed",
                    "activity.list",
                );
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "timeline temporarily unavailable",
                )
                    .into_response()
            }
        }
    }
}

// [COMMENT]: HTTP GET /api/v1/me/notification/list
// Lấy danh sách hộp thư thông báo (Notification Inbox) phân trang của người dùng hiện tại
pub async fn list_notifications(
    State(state): State<Arc<AppState>>,
    Extension(AuthUser(user_id)): Extension<AuthUser>,
    Query(request): Query<PageRequest>,
) -> Response {
    match state.inbox.list(user_id, request).await {
        Ok(page) => Json(page).into_response(),
        Err(error) => {
            let is_invalid = error.downcast_ref::<std::io::Error>().is_some_and(|e| {
                matches!(
                    e.kind(),
                    std::io::ErrorKind::InvalidInput | std::io::ErrorKind::InvalidData
                )
            });
            if is_invalid {
                Logger::sys_warn(
                    "http.timeline_invalid",
                    "Rejected invalid timeline request",
                    "notification.list",
                );
                (StatusCode::BAD_REQUEST, "invalid timeline request").into_response()
            } else {
                Logger::sys_error(
                    "http.timeline_failed",
                    "Timeline dependency request failed",
                    "notification.list",
                );
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "timeline temporarily unavailable",
                )
                    .into_response()
            }
        }
    }
}

// [COMMENT]: HTTP POST /api/v1/me/notification/:id/read
// Đánh dấu một thông báo cụ thể là đã đọc (read) trong ScyllaDB
pub async fn mark_notification_read(
    State(state): State<Arc<AppState>>,
    Extension(AuthUser(user_id)): Extension<AuthUser>,
    Path(id): Path<Uuid>,
    payload: Result<Json<MarkReadRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let payload = match payload {
        Ok(Json(payload)) => payload,
        Err(_) => {
            Logger::sys_warn(
                "http.timeline_invalid",
                "Rejected malformed mark read payload",
                "notification.read",
            );
            return (StatusCode::BAD_REQUEST, "invalid mark read payload").into_response();
        }
    };

    match state.inbox.mark_read(user_id, payload.created_at, id).await {
        Ok(()) => (StatusCode::OK, "marked as read").into_response(),
        Err(error) => {
            let is_invalid = error.downcast_ref::<std::io::Error>().is_some_and(|e| {
                matches!(
                    e.kind(),
                    std::io::ErrorKind::InvalidInput | std::io::ErrorKind::InvalidData
                )
            });
            if is_invalid {
                Logger::sys_warn(
                    "http.timeline_invalid",
                    "Rejected invalid timeline read mutation",
                    "notification.read",
                );
                (StatusCode::BAD_REQUEST, "invalid timeline request").into_response()
            } else {
                Logger::sys_error(
                    "http.timeline_failed",
                    "Timeline mark read mutation failed",
                    "notification.read",
                );
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "timeline temporarily unavailable",
                )
                    .into_response()
            }
        }
    }
}

// [COMMENT]: HTTP POST /api/v1/me/notification/read-all
// Đánh dấu tất cả thông báo trong hộp thư là đã đọc (cập nhật unread watermark trong ScyllaDB)
pub async fn mark_all_notifications_read(
    State(state): State<Arc<AppState>>,
    Extension(AuthUser(user_id)): Extension<AuthUser>,
) -> Response {
    match state.inbox.mark_all_read(user_id).await {
        Ok(()) => (StatusCode::OK, "marked all as read").into_response(),
        Err(error) => {
            let is_invalid = error.downcast_ref::<std::io::Error>().is_some_and(|e| {
                matches!(
                    e.kind(),
                    std::io::ErrorKind::InvalidInput | std::io::ErrorKind::InvalidData
                )
            });
            if is_invalid {
                Logger::sys_warn(
                    "http.timeline_invalid",
                    "Rejected invalid timeline read all mutation",
                    "notification.read_all",
                );
                (StatusCode::BAD_REQUEST, "invalid timeline request").into_response()
            } else {
                Logger::sys_error(
                    "http.timeline_failed",
                    "Timeline mark all read mutation failed",
                    "notification.read_all",
                );
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "timeline temporarily unavailable",
                )
                    .into_response()
            }
        }
    }
}
