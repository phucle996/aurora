use crate::api::{
    list_activities, list_notifications, mark_all_notifications_read, mark_notification_read,
};
use crate::app::state::AppState;
use crate::inbound::connect::handle_connect;
use axum::{
    extract::DefaultBodyLimit,
    routing::{get, post, put},
    Router,
};
use std::sync::Arc;
use tower_http::trace::TraceLayer;

pub fn build_router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/api/v1/realtime/connect", post(handle_connect))
        .route("/api/v1/me/activities", get(list_activities))
        .route("/api/v1/me/notifications", get(list_notifications))
        .route(
            "/api/v1/me/notifications/read-all",
            put(mark_all_notifications_read),
        )
        .route(
            "/api/v1/me/notifications/:id/read",
            put(mark_notification_read),
        )
        // Centrifugo connect payloads are small; this prevents an attacker from
        // allocating an oversized JSON body before schema validation runs.
        .layer(DefaultBodyLimit::max(64 * 1024))
        .layer(TraceLayer::new_for_http())
        .with_state(state)
}
