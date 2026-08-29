use crate::config::CentrifugoConfig;
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
use crate::service::ports::{AppError, RealtimePublisher};
use futures_util::future::BoxFuture;
use opentelemetry::trace::FutureExt;
use reqwest::Client;
use serde::Serialize;

#[derive(Clone)]
pub struct CentrifugoPublisher {
    client: Client,
    publish_url: String,
    api_key: String,
}

#[derive(Serialize)]
struct PublishRequest {
    channel: String,
    data: serde_json::Value,
}

impl CentrifugoPublisher {
    pub fn new(config: &CentrifugoConfig) -> Result<Self, reqwest::Error> {
        let client = Client::builder().timeout(config.request_timeout).build()?;
        let publish_url = if config.api_url.ends_with("/publish") {
            config.api_url.clone()
        } else {
            format!("{}/publish", config.api_url.trim_end_matches('/'))
        };

        Ok(Self {
            client,
            publish_url,
            api_key: config.api_key.clone(),
        })
    }

    async fn publish_json(
        &self,
        channel: &str,
        data: serde_json::Value,
    ) -> Result<(), reqwest::Error> {
        let payload = PublishRequest {
            channel: channel.to_owned(),
            data,
        };
        let trace_context = OtelTracer::start_current_span(
            "centrifugo.publish",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("http.request.method", "POST"),
                opentelemetry::KeyValue::new("server.address", "centrifugo"),
            ],
        );
        let propagation = OtelTracer::inject_context(&trace_context);
        let mut request = self
            .client
            .post(&self.publish_url)
            .header("X-API-Key", &self.api_key)
            .header("Authorization", format!("apikey {}", self.api_key))
            .json(&payload);
        if !propagation.traceparent.is_empty() {
            request = request.header("traceparent", propagation.traceparent);
        }
        if !propagation.tracestate.is_empty() {
            request = request.header("tracestate", propagation.tracestate);
        }

        let result = request
            .send()
            .with_context(trace_context.clone())
            .await
            .and_then(reqwest::Response::error_for_status)
            .map(|_| ());
        OtelTracer::finish_span(
            &trace_context,
            result.as_ref().err().map(|_| "CENTRIFUGO_PUBLISH_FAILED"),
        );
        result
    }
}

impl RealtimePublisher for CentrifugoPublisher {
    fn publish<'a>(
        &'a self,
        channel: &'a str,
        data: serde_json::Value,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            match self.publish_json(channel, data).await {
                Ok(()) => {
                    MetricsManager::record_centrifugo_publish("success");
                    Ok(())
                }
                Err(error) => {
                    MetricsManager::record_centrifugo_publish("failed");
                    Logger::sys_error(
                        "centrifugo.publish",
                        "Centrifugo publish failed",
                        &error.to_string(),
                    );
                    Err(Box::new(error) as AppError)
                }
            }
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::extract::State;
    use axum::http::{HeaderMap, StatusCode};
    use axum::routing::post;
    use axum::{Json, Router};
    use std::sync::Arc;
    use std::time::Duration;
    use tokio::sync::{oneshot, Mutex};

    type Capture = Arc<Mutex<Option<(HeaderMap, serde_json::Value)>>>;

    async fn capture_publish(
        State(capture): State<Capture>,
        headers: HeaderMap,
        Json(payload): Json<serde_json::Value>,
    ) -> StatusCode {
        *capture.lock().await = Some((headers, payload));
        StatusCode::OK
    }

    async fn rejected_publish() -> StatusCode {
        StatusCode::SERVICE_UNAVAILABLE
    }

    async fn server(router: Router) -> (String, oneshot::Sender<()>) {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("test listener");
        let address = listener.local_addr().expect("listener address");
        let (shutdown_tx, shutdown_rx) = oneshot::channel();
        tokio::spawn(async move {
            axum::serve(listener, router)
                .with_graceful_shutdown(async {
                    let _ = shutdown_rx.await;
                })
                .await
                .expect("test server");
        });
        (format!("http://{address}/api"), shutdown_tx)
    }

    #[tokio::test]
    async fn publish_uses_bounded_api_path_credentials_and_payload() {
        let capture = Arc::new(Mutex::new(None));
        let router = Router::new()
            .route("/api/publish", post(capture_publish))
            .with_state(capture.clone());
        let (api_url, shutdown) = server(router).await;
        let publisher = CentrifugoPublisher::new(&CentrifugoConfig {
            api_url,
            api_key: "test-api-key".to_string(),
            request_timeout: Duration::from_secs(1),
        })
        .expect("publisher");

        publisher
            .publish(
                "notifications:user-1",
                serde_json::json!({"notification_id": "notification-1"}),
            )
            .await
            .expect("publish");

        let (headers, body) = capture.lock().await.take().expect("captured request");
        assert_eq!(headers["x-api-key"], "test-api-key");
        assert_eq!(headers["authorization"], "apikey test-api-key");
        assert_eq!(body["channel"], "notifications:user-1");
        assert_eq!(body["data"]["notification_id"], "notification-1");
        let _ = shutdown.send(());
    }

    #[tokio::test]
    async fn non_success_response_is_a_delivery_failure() {
        let router = Router::new().route("/api/publish", post(rejected_publish));
        let (api_url, shutdown) = server(router).await;
        let publisher = CentrifugoPublisher::new(&CentrifugoConfig {
            api_url,
            api_key: "test-api-key".to_string(),
            request_timeout: Duration::from_secs(1),
        })
        .expect("publisher");

        assert!(publisher
            .publish("notifications:user-1", serde_json::json!({}))
            .await
            .is_err());
        let _ = shutdown.send(());
    }
}
