use crate::app::state::AppState;
use crate::inbound::connect::handle_connect;
use axum::{extract::DefaultBodyLimit, routing::post, Router};
use std::sync::Arc;
use tower_http::trace::TraceLayer;

pub fn build_router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/api/v1/realtime/connect", post(handle_connect))
        // Centrifugo connect payloads are small; this prevents an attacker from
        // allocating an oversized JSON body before schema validation runs.
        .layer(DefaultBodyLimit::max(64 * 1024))
        .layer(TraceLayer::new_for_http())
        .with_state(state)
}
