use crate::config::Config;
use opentelemetry::{global, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::metrics::{PeriodicReader, SdkMeterProvider};
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::{self, BatchConfigBuilder, Sampler};
use opentelemetry_sdk::Resource;
use std::error::Error;
use std::io;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;
use tracing_appender::non_blocking::{ErrorCounter, WorkerGuard};
use tracing_subscriber::filter::EnvFilter;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::util::SubscriberInitExt;
use tracing_subscriber::Layer;

mod logs;
pub mod metrics;
mod traces;

pub mod logger {
    pub use super::logs::*;
}

pub mod tracing {
    pub use super::traces::*;
}

pub const SERVICE_NAME: &str = "aurora-notification-service";
const SERVICE_NAMESPACE: &str = "aurora";
const DEFAULT_LOG_BUFFER_LINES: usize = 16_384;
const DEFAULT_TRACE_QUEUE_SIZE: usize = 8_192;
const DEFAULT_TRACE_BATCH_SIZE: usize = 512;
const DEFAULT_TRACE_SCHEDULE_DELAY_MS: u64 = 500;
const DEFAULT_EXPORT_TIMEOUT_SECS: u64 = 5;
const DEFAULT_METRIC_INTERVAL_SECS: u64 = 15;
const DEFAULT_TRACE_SAMPLE_RATIO: f64 = 0.10;
const LOG_STATS_INTERVAL_SECS: u64 = 10;

static INITIALIZED: AtomicBool = AtomicBool::new(false);

pub struct TelemetryRuntime;

pub struct TelemetryGuard {
    sampler: Option<tokio::task::JoinHandle<()>>,
    meter_provider: Option<SdkMeterProvider>,
    // WorkerGuard must outlive every final log emitted by Drop.
    _log_worker: WorkerGuard,
}

impl TelemetryRuntime {
    pub fn init(config: &Config) -> Result<TelemetryGuard, Box<dyn Error + Send + Sync + 'static>> {
        if INITIALIZED.swap(true, Ordering::AcqRel) {
            return Err(io::Error::new(
                io::ErrorKind::AlreadyExists,
                "telemetry runtime can only be initialized once",
            )
            .into());
        }

        let identity = ServiceIdentity::new();
        logs::install_identity(logs::LogIdentity {
            boot_id: identity.boot_id.clone(),
            deployment_environment: identity.deployment_environment.clone(),
            node_id: identity.node_id.clone(),
        })
        .map_err(|_| io::Error::new(io::ErrorKind::AlreadyExists, "log identity already set"))?;

        global::set_text_map_propagator(TraceContextPropagator::new());
        let resource = identity.resource();
        let export_timeout = Duration::from_secs(env_u64(
            "OTEL_EXPORT_TIMEOUT_SECS",
            DEFAULT_EXPORT_TIMEOUT_SECS,
            1,
            30,
        ));

        let (tracer, trace_error) = match build_tracer(config, resource.clone(), export_timeout) {
            Ok(tracer) => (Some(tracer), None),
            Err(error) => (None, Some(error.to_string())),
        };
        let (meter_provider, metric_error) =
            match build_meter_provider(config, resource, export_timeout) {
                Ok(provider) => {
                    global::set_meter_provider(provider.clone());
                    (Some(provider), None)
                }
                Err(error) => (None, Some(error.to_string())),
            };

        let queue_capacity = env_usize(
            "APP_LOG_BUFFERED_LINES",
            DEFAULT_LOG_BUFFER_LINES,
            1_024,
            262_144,
        );
        let (writer, log_worker) = tracing_appender::non_blocking::NonBlockingBuilder::default()
            .buffered_lines_limit(queue_capacity)
            .lossy(true)
            .thread_name("notification-log-writer")
            .finish(std::io::stdout());
        let dropped_lines = writer.error_counter();

        let log_filter = log_filter();
        let otel_filter = otel_filter();
        let fmt_layer = tracing_subscriber::fmt::layer()
            .json()
            .flatten_event(true)
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .with_current_span(false)
            .with_span_list(false)
            .with_writer(writer)
            .with_filter(log_filter);
        let otel_layer = tracer.map(|tracer| {
            tracing_opentelemetry::layer()
                .with_tracer(tracer)
                .with_filter(otel_filter)
        });

        tracing_subscriber::registry()
            .with(fmt_layer)
            .with(otel_layer)
            .try_init()?;

        // OTel export errors are diagnostic failures. Rate-limited structured
        // logs preserve visibility without recursively entering OTLP.
        let _ = global::set_error_handler(|error| {
            logger::Logger::sys_warn(
                "otel.export",
                "OpenTelemetry exporter reported an error",
                &error.to_string(),
            );
        });

        metrics::MetricsManager::init();
        if let Some(error) = trace_error {
            logger::Logger::sys_warn(
                "tracing.init",
                "Tracing exporter disabled; service remains available",
                &error,
            );
        }
        if let Some(error) = metric_error {
            logger::Logger::sys_warn(
                "metrics.init",
                "Metrics exporter disabled; service remains available",
                &error,
            );
        }
        logger::Logger::sys_info(
            "telemetry.init",
            "Bounded NDJSON logs and OpenTelemetry runtime initialized",
        );

        let sampler = spawn_log_stats_sampler(dropped_lines);
        Ok(TelemetryGuard {
            sampler: Some(sampler),
            meter_provider,
            _log_worker: log_worker,
        })
    }
}

impl Drop for TelemetryGuard {
    fn drop(&mut self) {
        logger::Logger::sys_info(
            "telemetry.shutdown",
            "Flushing bounded OpenTelemetry pipelines during graceful shutdown",
        );
        if let Some(sampler) = self.sampler.take() {
            sampler.abort();
        }

        // The exporter timeout bounds shutdown. Traces are shut down before
        // metrics, while the non-blocking log worker remains alive until all
        // providers have completed their final flush.
        global::shutdown_tracer_provider();
        if let Some(provider) = self.meter_provider.take() {
            if let Err(error) = provider.shutdown() {
                logger::Logger::sys_warn(
                    "metrics.shutdown",
                    "Metrics provider failed its final bounded flush",
                    &error.to_string(),
                );
            }
        }
    }
}

#[derive(Debug)]
struct ServiceIdentity {
    boot_id: String,
    deployment_environment: String,
    node_id: String,
}

impl ServiceIdentity {
    fn new() -> Self {
        Self {
            boot_id: uuid::Uuid::new_v4().to_string(),
            deployment_environment: std::env::var("DEPLOYMENT_ENVIRONMENT")
                .or_else(|_| std::env::var("APP_ENV"))
                .unwrap_or_else(|_| "unknown".to_owned()),
            node_id: traces::get_node_hostname(),
        }
    }

    fn resource(&self) -> Resource {
        Resource::new(vec![
            KeyValue::new("service.namespace", SERVICE_NAMESPACE),
            KeyValue::new("service.name", SERVICE_NAME),
            KeyValue::new("service.version", env!("CARGO_PKG_VERSION")),
            KeyValue::new("service.instance.id", self.boot_id.clone()),
            KeyValue::new(
                "deployment.environment.name",
                self.deployment_environment.clone(),
            ),
            KeyValue::new("host.name", self.node_id.clone()),
            KeyValue::new("process.pid", i64::from(std::process::id())),
            KeyValue::new("aurora.component.scope", "central"),
        ])
    }
}

fn build_tracer(
    config: &Config,
    resource: Resource,
    export_timeout: Duration,
) -> Result<opentelemetry_sdk::trace::Tracer, opentelemetry::trace::TraceError> {
    let queue_size = env_usize(
        "OTEL_TRACE_MAX_QUEUE_SIZE",
        DEFAULT_TRACE_QUEUE_SIZE,
        512,
        262_144,
    );
    let batch_size = env_usize(
        "OTEL_TRACE_MAX_EXPORT_BATCH_SIZE",
        DEFAULT_TRACE_BATCH_SIZE,
        1,
        queue_size,
    );
    let scheduled_delay = Duration::from_millis(env_u64(
        "OTEL_TRACE_SCHEDULED_DELAY_MS",
        DEFAULT_TRACE_SCHEDULE_DELAY_MS,
        50,
        10_000,
    ));
    let sample_ratio = env_f64(
        "OTEL_TRACE_SAMPLE_RATIO",
        DEFAULT_TRACE_SAMPLE_RATIO,
        0.0,
        1.0,
    );
    let batch_config = BatchConfigBuilder::default()
        .with_max_queue_size(queue_size)
        .with_max_export_batch_size(batch_size)
        .with_max_concurrent_exports(1)
        .with_scheduled_delay(scheduled_delay)
        .with_max_export_timeout(export_timeout)
        .build();
    let trace_config = trace::Config::default()
        .with_sampler(Sampler::ParentBased(Box::new(Sampler::TraceIdRatioBased(
            sample_ratio,
        ))))
        .with_max_attributes_per_span(32)
        .with_max_events_per_span(32)
        .with_max_links_per_span(32)
        .with_max_attributes_per_event(16)
        .with_max_attributes_per_link(8)
        .with_resource(resource);
    let exporter = opentelemetry_otlp::new_exporter()
        .tonic()
        .with_endpoint(&config.otel.exporter_endpoint)
        .with_timeout(export_timeout);

    opentelemetry_otlp::new_pipeline()
        .tracing()
        .with_exporter(exporter)
        .with_trace_config(trace_config)
        .with_batch_config(batch_config)
        .install_batch(opentelemetry_sdk::runtime::Tokio)
}

fn build_meter_provider(
    config: &Config,
    resource: Resource,
    export_timeout: Duration,
) -> Result<SdkMeterProvider, opentelemetry::metrics::MetricsError> {
    let exporter = opentelemetry_otlp::new_exporter()
        .tonic()
        .with_endpoint(&config.otel.exporter_endpoint)
        .with_timeout(export_timeout)
        .build_metrics_exporter(
            Box::new(opentelemetry_sdk::metrics::reader::DefaultAggregationSelector::new()),
            Box::new(opentelemetry_sdk::metrics::reader::DefaultTemporalitySelector::new()),
        )?;
    let interval = Duration::from_secs(env_u64(
        "OTEL_METRIC_EXPORT_INTERVAL_SECS",
        DEFAULT_METRIC_INTERVAL_SECS,
        5,
        300,
    ));
    let reader = PeriodicReader::builder(exporter, opentelemetry_sdk::runtime::Tokio)
        .with_interval(interval)
        .with_timeout(export_timeout)
        .build();
    Ok(SdkMeterProvider::builder()
        .with_reader(reader)
        .with_resource(resource)
        .build())
}

fn spawn_log_stats_sampler(dropped_lines: ErrorCounter) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(Duration::from_secs(LOG_STATS_INTERVAL_SECS));
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut previous_attempts = 0;
        let mut previous_suppressed = 0;
        let mut previous_dropped = 0;

        loop {
            interval.tick().await;
            let stats = logs::stats();
            let dropped = dropped_lines.dropped_lines() as u64;
            metrics::MetricsManager::record_log_pipeline(
                stats.attempts.saturating_sub(previous_attempts),
                dropped.saturating_sub(previous_dropped),
                stats.suppressed.saturating_sub(previous_suppressed),
            );
            previous_attempts = stats.attempts;
            previous_suppressed = stats.suppressed;
            previous_dropped = dropped;
        }
    })
}

fn log_filter() -> EnvFilter {
    let service_level = match std::env::var("APP_LOG_LEVEL")
        .unwrap_or_else(|_| "info".to_owned())
        .to_ascii_lowercase()
        .as_str()
    {
        "trace" => "trace",
        "debug" => "debug",
        "warn" => "warn",
        "error" => "error",
        _ => "info",
    };
    // Dependency INFO logs can be extremely noisy and duplicate the explicit
    // access contract. Keep them at WARN while retaining the requested level
    // for this service; APP_LOG_FILTER remains the expert override.
    let dependency_level = match service_level {
        "error" => "error",
        _ => "warn",
    };
    let fallback = format!("{dependency_level},notification_service={service_level}");
    let directive = std::env::var("APP_LOG_FILTER").unwrap_or(fallback);
    EnvFilter::try_new(directive)
        .unwrap_or_else(|_| EnvFilter::new("warn,notification_service=info"))
}

fn otel_filter() -> EnvFilter {
    let directive = std::env::var("OTEL_TRACE_FILTER")
        .unwrap_or_else(|_| "notification_service=info,tower_http=info".to_owned());
    EnvFilter::try_new(directive)
        .unwrap_or_else(|_| EnvFilter::new("notification_service=info,tower_http=info"))
}

fn env_usize(key: &str, default: usize, min: usize, max: usize) -> usize {
    std::env::var(key)
        .ok()
        .and_then(|value| value.parse().ok())
        .unwrap_or(default)
        .clamp(min, max)
}

fn env_u64(key: &str, default: u64, min: u64, max: u64) -> u64 {
    std::env::var(key)
        .ok()
        .and_then(|value| value.parse().ok())
        .unwrap_or(default)
        .clamp(min, max)
}

fn env_f64(key: &str, default: f64, min: f64, max: f64) -> f64 {
    std::env::var(key)
        .ok()
        .and_then(|value| value.parse().ok())
        .filter(|value: &f64| value.is_finite())
        .unwrap_or(default)
        .clamp(min, max)
}
