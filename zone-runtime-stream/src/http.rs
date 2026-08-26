use std::{convert::Infallible, sync::Arc};

use async_stream::stream;
use axum::{
    extract::{Query, State},
    http::{header, HeaderMap, StatusCode},
    response::{
        sse::{Event, Sse},
        IntoResponse,
    },
    routing::get,
    Router,
};
use futures_util::Stream;
use serde_json::json;
use tokio::time::sleep;
use uuid::Uuid;

use crate::{
    contract::{RuntimeFrame, RuntimeScope, StreamQuery},
    stream::{next_event_id, RuntimeStream},
};

pub fn router(runtime: RuntimeStream) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/metrics", get(metrics))
        .route("/runtime/stream", get(runtime_stream))
        .with_state(runtime)
}

async fn healthz(State(runtime): State<RuntimeStream>) -> impl IntoResponse {
    let _ = runtime;
    (StatusCode::OK, "ok")
}

async fn metrics(State(runtime): State<RuntimeStream>) -> impl IntoResponse {
    (
        [(header::CONTENT_TYPE, "text/plain; version=0.0.4")],
        runtime.prometheus_metrics(),
    )
}

async fn runtime_stream(
    State(runtime): State<RuntimeStream>,
    headers: HeaderMap,
    Query(query): Query<StreamQuery>,
) -> impl IntoResponse {
    let zone_id = match required_uuid_header(&headers, "x-aurora-zone-id") {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    let module = match trusted_token(&headers, "x-aurora-module", 64) {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    let resource_type = match trusted_token(&headers, "x-aurora-resource-type", 64) {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    let resource_id = match required_uuid_header(&headers, "x-aurora-resource-id") {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    let owner_id = match required_uuid_header(&headers, "x-aurora-owner-id") {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    let workspace_id = match required_uuid_header(&headers, "x-aurora-workspace-id") {
        Ok(value) => value,
        Err(response) => return response.into_response(),
    };
    if query.panel_id.is_some() || query.component_id.is_some() {
        return bad_request("panel and component scope must come from trusted headers")
            .into_response();
    }
    let panel_id = header_string(&headers, "x-aurora-panel-id");
    let panel_id = match panel_id.filter(|value| !value.trim().is_empty()) {
        Some(value) => value,
        None => return bad_request("panel_id is required").into_response(),
    };
    let component_id = header_string(&headers, "x-aurora-component-id");
    let snapshot_seconds = query
        .from_seconds
        .unwrap_or_else(|| runtime.max_snapshot().as_secs().min(60))
        .max(1);
    let resumed = match headers
        .get("last-event-id")
        .and_then(|value| value.to_str().ok())
    {
        Some(value)
            if value.is_empty()
                || value.len() > 128
                || !value.bytes().all(|byte| {
                    byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.')
                }) =>
        {
            return bad_request("last-event-id is invalid").into_response();
        }
        Some(_) => true,
        None => false,
    };
    let scope = RuntimeScope {
        module,
        resource_type,
        resource_id,
        owner_id,
        workspace_id,
        zone_id,
        component_id,
        panel_id,
        snapshot_seconds,
    };
    if let Err(error) = scope.validate(runtime.zone_id()) {
        tracing::warn!(event_code = "ZONE_RUNTIME_STREAM_SCOPE_REJECTED", reason = %error);
        return forbidden("runtime scope rejected").into_response();
    }
    if snapshot_seconds > runtime.max_snapshot().as_secs() {
        return bad_request("from_seconds exceeds the stream snapshot budget").into_response();
    }

    let (receiver, permit, subscription) = match runtime.subscribe(scope.clone()).await {
        Ok(value) => value,
        Err(_) => return too_many_requests().into_response(),
    };
    // Access audit deliberately omits owner/workspace/resource identity and
    // ticket material. Module/resource type/panel are bounded registries.
    tracing::info!(
        event_code = "ZONE_RUNTIME_STREAM_OPENED",
        module = %scope.module,
        resource_type = %scope.resource_type,
        panel = %scope.panel_id,
        outcome = "allowed"
    );
    let subscription_guard = SubscriptionGuard::new(runtime.clone(), scope.clone(), subscription);
    let stream = event_stream(
        runtime,
        scope,
        receiver,
        permit,
        subscription_guard,
        resumed,
    );
    let mut response = Sse::new(stream).into_response();
    let headers = response.headers_mut();
    headers.insert(header::CACHE_CONTROL, "no-store".parse().unwrap());
    headers.insert("x-content-type-options", "nosniff".parse().unwrap());
    headers.insert("x-accel-buffering", "no".parse().unwrap());
    response
}

fn event_stream(
    runtime: RuntimeStream,
    scope: RuntimeScope,
    mut receiver: tokio::sync::broadcast::Receiver<RuntimeFrame>,
    permit: tokio::sync::OwnedSemaphorePermit,
    subscription_guard: SubscriptionGuard,
    resumed: bool,
) -> impl Stream<Item = Result<Event, Infallible>> {
    stream! {
        let _permit = permit;
        // The guard was created before the response body. Even if the body is
        // dropped before its first poll, it still releases the fan-out client
        // reference and connection accounting from Drop.
        let _subscription_guard = subscription_guard;
        if resumed {
            yield Ok(Event::default().event("runtime.gap").id(next_event_id()).json_data(json!({"reason": "cursor_not_replayed"})).unwrap());
        }
        let deadline = sleep(runtime.max_lifetime());
        tokio::pin!(deadline);
        let mut heartbeat = tokio::time::interval(runtime.heartbeat());
        loop {
            tokio::select! {
                _ = &mut deadline => {
                    runtime.stream_expired();
                    tracing::info!(
                        event_code = "ZONE_RUNTIME_STREAM_EXPIRED",
                        module = %scope.module,
                        resource_type = %scope.resource_type,
                        panel = %scope.panel_id,
                        outcome = "expired"
                    );
                    yield Ok(Event::default().event("stream.error").id(next_event_id()).json_data(json!({"code": "STREAM_EXPIRED"})).unwrap());
                    break;
                }
                _ = heartbeat.tick() => {
                    yield Ok(Event::default().event("heartbeat").id(next_event_id()).data("{}"));
                }
                received = receiver.recv() => {
                    match received {
                        Ok(frame) => {
                            let event_type = frame.event_type();
                            let event_id = frame.event_id().to_string();
                            let data = frame.data().unwrap_or_else(|_| "{\"code\":\"FRAME_SERIALIZATION_FAILED\"}".into());
                            yield Ok(Event::default().event(event_type).id(event_id).data(data));
                        }
                        Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => {
                            runtime.gap_event();
                            if scope.panel_id == "logs" {
                                // Logs are ordered records: dropping arbitrary lines and
                                // continuing would present a false complete tail. Close so
                                // the browser reconnects with a bounded snapshot window.
                                yield Ok(Event::default().event("stream.error").id(next_event_id()).json_data(json!({"code": "BACKPRESSURE"})).unwrap());
                                break;
                            }
                            // Metrics/state are soft snapshots. Drain the bounded receiver
                            // and deliver only the newest frame after declaring the gap.
                            let mut latest = None;
                            loop {
                                match receiver.try_recv() {
                                    Ok(frame) => latest = Some(frame),
                                    Err(tokio::sync::broadcast::error::TryRecvError::Lagged(_)) => continue,
                                    Err(tokio::sync::broadcast::error::TryRecvError::Empty)
                                    | Err(tokio::sync::broadcast::error::TryRecvError::Closed) => break,
                                }
                            }
                            yield Ok(Event::default().event("runtime.gap").id(next_event_id()).json_data(json!({"reason": "backpressure"})).unwrap());
                            if let Some(frame) = latest {
                                let event_type = frame.event_type();
                                let event_id = frame.event_id().to_string();
                                let data = frame.data().unwrap_or_else(|_| "{\"code\":\"FRAME_SERIALIZATION_FAILED\"}".into());
                                yield Ok(Event::default().event(event_type).id(event_id).data(data));
                            }
                        }
                        Err(tokio::sync::broadcast::error::RecvError::Closed) => break,
                    }
                }
            }
        }
    }
}

struct SubscriptionGuard {
    runtime: RuntimeStream,
    scope: RuntimeScope,
    subscription: Arc<crate::stream::Subscription>,
}

impl SubscriptionGuard {
    fn new(
        runtime: RuntimeStream,
        scope: RuntimeScope,
        subscription: Arc<crate::stream::Subscription>,
    ) -> Self {
        Self {
            runtime,
            scope,
            subscription,
        }
    }
}

impl Drop for SubscriptionGuard {
    fn drop(&mut self) {
        self.runtime.connection_closed();
        tracing::info!(
            event_code = "ZONE_RUNTIME_STREAM_CLOSED",
            module = %self.scope.module,
            resource_type = %self.scope.resource_type,
            panel = %self.scope.panel_id,
            outcome = "closed"
        );
        if self
            .subscription
            .clients
            .fetch_sub(1, std::sync::atomic::Ordering::AcqRel)
            == 1
        {
            let runtime = self.runtime.clone();
            let scope = self.scope.clone();
            let subscription = self.subscription.clone();
            tokio::spawn(async move {
                runtime.remove_if_unused(&scope, &subscription).await;
            });
        }
    }
}

fn header_string(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
}

fn required_uuid_header(headers: &HeaderMap, name: &str) -> Result<Uuid, ResponseError> {
    let value = header_string(headers, name)
        .ok_or_else(|| bad_request("required runtime scope header is missing"))?;
    Uuid::parse_str(&value).map_err(|_| bad_request("runtime scope identity is invalid"))
}

fn trusted_token(
    headers: &HeaderMap,
    name: &str,
    max_length: usize,
) -> Result<String, ResponseError> {
    let value = header_string(headers, name)
        .ok_or_else(|| bad_request("required runtime scope header is missing"))?;
    if value.is_empty()
        || value.len() > max_length
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(bad_request("runtime scope token is invalid"));
    }
    Ok(value)
}

#[derive(Clone)]
struct ResponseError {
    status: StatusCode,
    message: &'static str,
}

impl IntoResponse for ResponseError {
    fn into_response(self) -> axum::response::Response {
        (self.status, self.message).into_response()
    }
}

fn bad_request(message: &'static str) -> ResponseError {
    ResponseError {
        status: StatusCode::BAD_REQUEST,
        message,
    }
}

fn forbidden(message: &'static str) -> ResponseError {
    ResponseError {
        status: StatusCode::FORBIDDEN,
        message,
    }
}

fn too_many_requests() -> ResponseError {
    ResponseError {
        status: StatusCode::TOO_MANY_REQUESTS,
        message: "runtime stream capacity is exhausted",
    }
}
