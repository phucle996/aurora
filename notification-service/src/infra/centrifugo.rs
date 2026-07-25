use crate::application::ports::{AppError, RealtimePublisher};
use crate::config::CentrifugoConfig;
use crate::observability::{logger::Logger, metrics::MetricsManager, tracing::OtelTracer};
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
