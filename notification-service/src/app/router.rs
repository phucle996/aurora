use crate::handler::connect::{handle_connect, AppState};
use axum::{routing::post, Router};
use std::sync::Arc;
use tower_http::trace::TraceLayer;

// Xây dựng router chính của Axum và cấu hình middleware trace cho các request HTTP
pub fn build_router(app_state: Arc<AppState>) -> Router {
    // [ignoring loop detection]
    Router::new()
        .route("/api/v1/realtime/connect", post(handle_connect))
        .layer(TraceLayer::new_for_http())
        .with_state(app_state)
}
