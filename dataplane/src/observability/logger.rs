use std::borrow::Cow;
use std::collections::HashMap;
use std::hash::{DefaultHasher, Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};

use opentelemetry::metrics::Counter;
use opentelemetry::{global, KeyValue};
use tracing_appender::non_blocking::{ErrorCounter, NonBlockingBuilder, WorkerGuard};
use tracing_subscriber::EnvFilter;

const SERVICE_NAME: &str = "aurora-dataplane";
const DEFAULT_MAX_FIELD_BYTES: usize = 16 * 1024;
const DEFAULT_BUFFERED_LINES: usize = 16 * 1024;
const DEFAULT_RATE_LIMIT_MS: u64 = 5_000;
const MAX_RATE_LIMIT_KEYS: usize = 2_048;

static SETTINGS: OnceLock<LoggerSettings> = OnceLock::new();
static CONTEXT: OnceLock<LoggerContext> = OnceLock::new();
static EMITTED_LINES: AtomicU64 = AtomicU64::new(0);
static SUPPRESSED_LINES: AtomicU64 = AtomicU64::new(0);
static RATE_LIMITER: OnceLock<Mutex<HashMap<u64, RateLimitEntry>>> = OnceLock::new();
static APPENDER_ERROR_COUNTER: OnceLock<ErrorCounter> = OnceLock::new();
static LOGS_DROPPED_COUNTER: OnceLock<Counter<u64>> = OnceLock::new();
static LOGS_SUPPRESSED_COUNTER: OnceLock<Counter<u64>> = OnceLock::new();
static LOGS_EMITTED_COUNTER: OnceLock<Counter<u64>> = OnceLock::new();
static LAST_RECORDED_DROPPED: AtomicU64 = AtomicU64::new(0);
static LAST_RECORDED_SUPPRESSED: AtomicU64 = AtomicU64::new(0);
static LAST_RECORDED_EMITTED: AtomicU64 = AtomicU64::new(0);

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum LogLevel {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

impl LogLevel {
    pub fn from_str(value: &str) -> Self {
        match value.to_ascii_lowercase().as_str() {
            "debug" => Self::Debug,
            "warn" | "warning" => Self::Warn,
            "error" => Self::Error,
            _ => Self::Info,
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Debug => "debug",
            Self::Info => "info",
            Self::Warn => "warn",
            Self::Error => "error",
        }
    }
}

struct LoggerSettings {
    level: LogLevel,
    max_field_bytes: usize,
    rate_limit: Duration,
}

impl LoggerSettings {
    fn load() -> Self {
        Self {
            level: LogLevel::from_str(
                &std::env::var("APP_LOG_LEVEL").unwrap_or_else(|_| "info".to_string()),
            ),
            max_field_bytes: parse_bounded_env(
                "APP_LOG_MAX_FIELD_BYTES",
                DEFAULT_MAX_FIELD_BYTES,
                1_024,
                256 * 1024,
            ),
            rate_limit: Duration::from_millis(parse_bounded_env(
                "APP_LOG_RATE_LIMIT_MS",
                DEFAULT_RATE_LIMIT_MS,
                100,
                60_000,
            )),
        }
    }
}

struct LoggerContext {
    service_version: String,
    node_id: String,
    boot_id: String,
}

impl LoggerContext {
    fn load() -> Self {
        let node_id = hostname::get()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
        Self {
            service_version: std::env::var("APP_VERSION")
                .ok()
                .filter(|value| !value.trim().is_empty())
                .unwrap_or_else(|| env!("CARGO_PKG_VERSION").to_string()),
            node_id,
            // A process incarnation remains distinct from business event_id, which is stable
            // across retries/failover.
            boot_id: uuid::Uuid::new_v4().to_string(),
        }
    }
}

struct RateLimitEntry {
    last_emitted: Instant,
    suppressed: u64,
}

/// Optional typed context for HA/Kafka events. Empty fields stay explicit in the JSON schema so
/// collectors do not have to infer values from a human message.
#[derive(Clone, Copy, Default)]
pub struct LogFields<'a> {
    pub event_id: Option<&'a str>,
    pub operation_id: Option<&'a str>,
    pub job_version: Option<u64>,
    pub leader_fencing_token: Option<u64>,
    pub fencing_token: Option<u64>,
    pub runtime_generation: Option<u64>,
    pub slot: Option<u32>,
    pub kafka_topic: Option<&'a str>,
    pub kafka_partition: Option<i32>,
    pub kafka_offset: Option<i64>,
    pub assignment_epoch: Option<u64>,
    pub retryable: Option<bool>,
    pub duration_ms: Option<u64>,
    pub outcome: Option<&'a str>,
}

/// Owns the non-blocking writer thread. Keeping this guard in `main` is the durability boundary
/// that flushes queued records on normal shutdown and on bootstrap errors that unwind.
#[must_use = "dropping LoggerGuard immediately stops the asynchronous log writer"]
pub struct LoggerGuard {
    worker_guard: Option<WorkerGuard>,
}

impl Drop for LoggerGuard {
    fn drop(&mut self) {
        if self.worker_guard.is_some() {
            Logger::emit_shutdown_summary();
        }
        // WorkerGuard is dropped after the summary is queued and joins the writer thread.
        self.worker_guard.take();
    }
}

pub struct Logger;

impl Logger {
    /// Installs exactly one JSON subscriber and a bounded, lossy writer.
    ///
    /// Loss under collector backpressure is observable through `dataplane_logs_dropped_total`;
    /// blocking a Tokio executor thread is deliberately forbidden because logs are diagnostic,
    /// while Kafka/result durability remains the business correctness boundary.
    pub fn init() -> LoggerGuard {
        let settings = SETTINGS.get_or_init(LoggerSettings::load);
        let context = CONTEXT.get_or_init(LoggerContext::load);
        let buffered_lines = parse_bounded_env(
            "APP_LOG_BUFFERED_LINES",
            DEFAULT_BUFFERED_LINES,
            1_024,
            262_144,
        );
        let (writer, worker_guard) = NonBlockingBuilder::default()
            .buffered_lines_limit(buffered_lines)
            .lossy(true)
            .thread_name("aurora-dataplane-log-writer")
            .finish(std::io::stdout());
        let _ = APPENDER_ERROR_COUNTER.set(writer.error_counter());

        let filter = EnvFilter::try_new(format!("{SERVICE_NAME}={}", settings.level.as_str()))
            .expect("static Dataplane log filter must be valid");
        let subscriber = tracing_subscriber::fmt()
            .json()
            .flatten_event(true)
            .with_ansi(false)
            .with_current_span(false)
            .with_span_list(false)
            .with_target(false)
            .with_level(false)
            .with_thread_ids(false)
            .with_thread_names(false)
            .with_file(false)
            .with_line_number(false)
            .with_env_filter(filter)
            .with_writer(writer)
            .finish();

        tracing::subscriber::set_global_default(subscriber)
            .expect("Dataplane structured logger must be initialized exactly once");

        Self::sys_info_with_fields(
            "logger.init",
            "LOGGER_INITIALIZED",
            &format!(
                "Structured JSON logger initialized; level={}, buffer_lines={}, max_field_bytes={}",
                settings.level.as_str(),
                buffered_lines,
                settings.max_field_bytes
            ),
            LogFields {
                outcome: Some("ready"),
                ..LogFields::default()
            },
        );
        Self::sys_info_with_fields(
            "logger.identity",
            "LOGGER_PROCESS_IDENTITY",
            &format!(
                "Dataplane log identity initialized for node={} boot={}",
                context.node_id, context.boot_id
            ),
            LogFields {
                outcome: Some("ready"),
                ..LogFields::default()
            },
        );

        LoggerGuard {
            worker_guard: Some(worker_guard),
        }
    }

    fn get_level() -> LogLevel {
        SETTINGS.get_or_init(LoggerSettings::load).level
    }

    pub fn boot_id() -> &'static str {
        &CONTEXT.get_or_init(LoggerContext::load).boot_id
    }

    pub fn service_version() -> &'static str {
        &CONTEXT.get_or_init(LoggerContext::load).service_version
    }

    pub fn node_id() -> &'static str {
        &CONTEXT.get_or_init(LoggerContext::load).node_id
    }

    pub fn dropped_lines() -> u64 {
        APPENDER_ERROR_COUNTER
            .get()
            .map(|counter| counter.dropped_lines() as u64)
            .unwrap_or_default()
    }

    /// Records cumulative process-local pipeline health. The boot_id on logs disambiguates
    /// counter resets after pod restart.
    pub fn record_pipeline_metrics(zone_id: &str) {
        let attributes = [KeyValue::new("zone_id", zone_id.to_string())];
        record_counter_delta(
            logs_dropped_counter(),
            &LAST_RECORDED_DROPPED,
            Self::dropped_lines(),
            &attributes,
        );
        record_counter_delta(
            logs_suppressed_counter(),
            &LAST_RECORDED_SUPPRESSED,
            SUPPRESSED_LINES.load(Ordering::Relaxed),
            &attributes,
        );
        record_counter_delta(
            logs_emitted_counter(),
            &LAST_RECORDED_EMITTED,
            EMITTED_LINES.load(Ordering::Relaxed),
            &attributes,
        );
    }

    pub fn sys_debug(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Debug {
            Self::emit(
                LogLevel::Debug,
                op,
                "SYSTEM_DEBUG",
                message,
                "",
                LogFields::default(),
                false,
            );
        }
    }

    pub fn sys_info(op: &str, message: &str) {
        Self::sys_info_with_fields(
            op,
            "SYSTEM_INFO",
            message,
            LogFields {
                outcome: Some("observed"),
                ..LogFields::default()
            },
        );
    }

    pub fn sys_info_with_fields(op: &str, event_code: &str, message: &str, fields: LogFields<'_>) {
        if Self::get_level() <= LogLevel::Info {
            Self::emit(LogLevel::Info, op, event_code, message, "", fields, false);
        }
    }

    pub fn sys_warn(op: &str, message: &str, error: &str) {
        let error_is_code = is_stable_error_code(error);
        Self::sys_warn_with_fields(
            op,
            if error_is_code {
                error
            } else {
                "SYSTEM_WARNING"
            },
            message,
            if error_is_code { "" } else { error },
            LogFields::default(),
        );
    }

    pub fn sys_warn_with_fields(
        op: &str,
        event_code: &str,
        message: &str,
        error: &str,
        fields: LogFields<'_>,
    ) {
        if Self::get_level() <= LogLevel::Warn {
            Self::emit(LogLevel::Warn, op, event_code, message, error, fields, true);
        }
    }

    pub fn sys_error(op: &str, message: &str, error: &str) {
        let error_is_code = is_stable_error_code(error);
        Self::sys_error_with_fields(
            op,
            if error_is_code { error } else { "SYSTEM_ERROR" },
            message,
            if error_is_code { "" } else { error },
            LogFields::default(),
        );
    }

    pub fn sys_error_with_fields(
        op: &str,
        event_code: &str,
        message: &str,
        error: &str,
        fields: LogFields<'_>,
    ) {
        if Self::get_level() <= LogLevel::Error {
            Self::emit(
                LogLevel::Error,
                op,
                event_code,
                message,
                error,
                fields,
                true,
            );
        }
    }

    pub fn job_log(job_id: &str, job_topic: &str, attempt: u32, op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            Self::emit_job(
                job_id,
                job_topic,
                attempt,
                op,
                "JOB_LIFECYCLE_EVENT",
                message,
                LogFields {
                    operation_id: Some(job_id),
                    outcome: Some("observed"),
                    ..LogFields::default()
                },
            );
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn emit(
        level: LogLevel,
        op: &str,
        event_code: &str,
        message: &str,
        error: &str,
        fields: LogFields<'_>,
        rate_limited: bool,
    ) {
        let message = protected_field(message);
        let error = protected_field(error);
        let op = bounded_field(op);
        let event_code = bounded_field(event_code);
        let suppressed_count = if rate_limited {
            match rate_limit_decision(op.as_ref(), event_code.as_ref(), error.as_ref()) {
                Some(count) => count,
                None => return,
            }
        } else {
            0
        };
        let context = CONTEXT.get_or_init(LoggerContext::load);
        let trace_id =
            crate::observability::otel::OtelTracer::get_current_trace_id().unwrap_or_default();
        let span_id =
            crate::observability::otel::OtelTracer::get_current_span_id().unwrap_or_default();
        let event_id = bounded_field(fields.event_id.unwrap_or_default());
        let operation_id = bounded_field(fields.operation_id.unwrap_or_default());
        let kafka_topic = bounded_field(fields.kafka_topic.unwrap_or_default());
        let outcome = bounded_field(fields.outcome.unwrap_or_default());

        macro_rules! emit_at {
            ($tracing_level:expr) => {
                tracing::event!(
                    target: SERVICE_NAME,
                    $tracing_level,
                    level = level.as_str(),
                    service_name = SERVICE_NAME,
                    service_version = context.service_version.as_str(),
                    service_instance_id = context.boot_id.as_str(),
                    op = op.as_ref(),
                    event_code = event_code.as_ref(),
                    event_id = event_id.as_ref(),
                    operation_id = operation_id.as_ref(),
                    trace_id = trace_id.as_str(),
                    span_id = span_id.as_str(),
                    job_version = fields.job_version.unwrap_or_default(),
                    leader_fencing_token = fields.leader_fencing_token.unwrap_or_default(),
                    fencing_token = fields.fencing_token.unwrap_or_default(),
                    runtime_generation = fields.runtime_generation.unwrap_or_default(),
                    slot = fields.slot.unwrap_or_default(),
                    kafka_topic = kafka_topic.as_ref(),
                    kafka_partition = fields.kafka_partition.unwrap_or(-1),
                    kafka_offset = fields.kafka_offset.unwrap_or(-1),
                    assignment_epoch = fields.assignment_epoch.unwrap_or_default(),
                    retryable = fields.retryable.unwrap_or(false),
                    duration_ms = fields.duration_ms.unwrap_or_default(),
                    outcome = outcome.as_ref(),
                    suppressed_count,
                    message = message.as_ref(),
                    error = error.as_ref(),
                );
            };
        }
        match level {
            LogLevel::Debug => {
                emit_at!(tracing::Level::DEBUG);
            }
            LogLevel::Info => {
                emit_at!(tracing::Level::INFO);
            }
            LogLevel::Warn => {
                emit_at!(tracing::Level::WARN);
            }
            LogLevel::Error => {
                emit_at!(tracing::Level::ERROR);
            }
        }
        EMITTED_LINES.fetch_add(1, Ordering::Relaxed);
    }

    fn emit_job(
        job_id: &str,
        job_topic: &str,
        attempt: u32,
        op: &str,
        event_code: &str,
        message: &str,
        fields: LogFields<'_>,
    ) {
        let context = CONTEXT.get_or_init(LoggerContext::load);
        let trace_id =
            crate::observability::otel::OtelTracer::get_current_trace_id().unwrap_or_default();
        let span_id =
            crate::observability::otel::OtelTracer::get_current_span_id().unwrap_or_default();
        let message = protected_field(message);
        let job_id = bounded_field(job_id);
        let job_topic = bounded_field(job_topic);
        let op = bounded_field(op);
        let event_id = bounded_field(fields.event_id.unwrap_or_default());
        let operation_id = bounded_field(fields.operation_id.unwrap_or(job_id.as_ref()));
        let kafka_topic = bounded_field(fields.kafka_topic.unwrap_or_default());
        let outcome = bounded_field(fields.outcome.unwrap_or_default());

        tracing::event!(
            target: SERVICE_NAME,
            tracing::Level::INFO,
            level = "info",
            service_name = SERVICE_NAME,
            service_version = context.service_version.as_str(),
            service_instance_id = context.boot_id.as_str(),
            op = op.as_ref(),
            event_code,
            event_id = event_id.as_ref(),
            operation_id = operation_id.as_ref(),
            trace_id = trace_id.as_str(),
            span_id = span_id.as_str(),
            job_id = job_id.as_ref(),
            job_topic = job_topic.as_ref(),
            job_version = fields.job_version.unwrap_or_default(),
            attempt,
            leader_fencing_token = fields.leader_fencing_token.unwrap_or_default(),
            fencing_token = fields.fencing_token.unwrap_or_default(),
            runtime_generation = fields.runtime_generation.unwrap_or_default(),
            slot = fields.slot.unwrap_or_default(),
            kafka_topic = kafka_topic.as_ref(),
            kafka_partition = fields.kafka_partition.unwrap_or(-1),
            kafka_offset = fields.kafka_offset.unwrap_or(-1),
            assignment_epoch = fields.assignment_epoch.unwrap_or_default(),
            retryable = fields.retryable.unwrap_or(false),
            duration_ms = fields.duration_ms.unwrap_or_default(),
            outcome = outcome.as_ref(),
            suppressed_count = 0_u64,
            message = message.as_ref(),
            error = "",
        );
        EMITTED_LINES.fetch_add(1, Ordering::Relaxed);
    }

    fn emit_shutdown_summary() {
        Self::sys_info_with_fields(
            "logger.shutdown",
            "LOGGER_SHUTDOWN_SUMMARY",
            &format!(
                "Logger shutting down; emitted={}, suppressed={}, dropped={}",
                EMITTED_LINES.load(Ordering::Relaxed),
                SUPPRESSED_LINES.load(Ordering::Relaxed),
                Self::dropped_lines()
            ),
            LogFields {
                outcome: Some("flushed"),
                ..LogFields::default()
            },
        );
    }
}

fn bounded_field(value: &str) -> Cow<'_, str> {
    let max_bytes = SETTINGS.get_or_init(LoggerSettings::load).max_field_bytes;
    if value.len() <= max_bytes {
        return Cow::Borrowed(value);
    }
    let mut boundary = max_bytes;
    while boundary > 0 && !value.is_char_boundary(boundary) {
        boundary -= 1;
    }
    Cow::Owned(format!("{}...[truncated]", &value[..boundary]))
}

fn protected_field(value: &str) -> Cow<'_, str> {
    let bounded = bounded_field(value);
    if contains_sensitive_marker(bounded.as_ref()) {
        Cow::Borrowed("[REDACTED_SENSITIVE_LOG_FIELD]")
    } else {
        bounded
    }
}

pub(crate) fn sanitize_for_durable_event(value: &str, max_bytes: usize) -> String {
    if contains_sensitive_marker(value) {
        return "[REDACTED_SENSITIVE_EVENT_FIELD]".to_string();
    }
    if value.len() <= max_bytes {
        return value.to_string();
    }

    const SUFFIX: &str = "...[truncated]";
    if max_bytes <= SUFFIX.len() {
        return SUFFIX[..max_bytes].to_string();
    }
    let content_limit = max_bytes.saturating_sub(SUFFIX.len());
    let mut boundary = content_limit.min(value.len());
    while boundary > 0 && !value.is_char_boundary(boundary) {
        boundary -= 1;
    }
    format!("{}{SUFFIX}", &value[..boundary])
}

fn contains_sensitive_marker(value: &str) -> bool {
    const MARKERS: &[&[u8]] = &[
        b"authorization:",
        b"authorization=",
        b"bearer ",
        b"password:",
        b"password=",
        b"secret:",
        b"secret=",
        b"pveapitoken=",
        b"x-amz-security-token",
        b"mail_stream_envelope_key",
        b"kafka_password",
        b"proxmox_api_token",
        b"stalwart_jmap_bearer_token",
    ];
    let bytes = value.as_bytes();
    MARKERS.iter().any(|marker| {
        bytes
            .windows(marker.len())
            .any(|window| window.eq_ignore_ascii_case(marker))
    })
}

fn is_stable_error_code(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.' | b':'))
}

fn rate_limit_decision(op: &str, event_code: &str, error: &str) -> Option<u64> {
    let mut hasher = DefaultHasher::new();
    op.hash(&mut hasher);
    event_code.hash(&mut hasher);
    error.hash(&mut hasher);
    let key = hasher.finish();
    let now = Instant::now();
    let window = SETTINGS.get_or_init(LoggerSettings::load).rate_limit;
    let limiter = RATE_LIMITER.get_or_init(|| Mutex::new(HashMap::new()));
    let mut entries = limiter
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());

    if let Some(entry) = entries.get_mut(&key) {
        if now.duration_since(entry.last_emitted) < window {
            entry.suppressed = entry.suppressed.saturating_add(1);
            SUPPRESSED_LINES.fetch_add(1, Ordering::Relaxed);
            return None;
        }
        let suppressed = std::mem::take(&mut entry.suppressed);
        entry.last_emitted = now;
        return Some(suppressed);
    }

    // [COMMENT]: Malformed external input must not create an unbounded in-memory cardinality map.
    if entries.len() >= MAX_RATE_LIMIT_KEYS {
        entries.retain(|_, entry| now.duration_since(entry.last_emitted) < window);
        if entries.len() >= MAX_RATE_LIMIT_KEYS {
            SUPPRESSED_LINES.fetch_add(1, Ordering::Relaxed);
            return None;
        }
    }
    entries.insert(
        key,
        RateLimitEntry {
            last_emitted: now,
            suppressed: 0,
        },
    );
    Some(0)
}

fn logs_dropped_counter() -> &'static Counter<u64> {
    LOGS_DROPPED_COUNTER.get_or_init(|| {
        global::meter(SERVICE_NAME)
            .u64_counter("dataplane_logs_dropped_total")
            .with_description("Cumulative log records dropped by the bounded non-blocking writer")
            .init()
    })
}

fn logs_suppressed_counter() -> &'static Counter<u64> {
    LOGS_SUPPRESSED_COUNTER.get_or_init(|| {
        global::meter(SERVICE_NAME)
            .u64_counter("dataplane_logs_suppressed_total")
            .with_description("Cumulative duplicate warn/error records suppressed by rate limiting")
            .init()
    })
}

fn logs_emitted_counter() -> &'static Counter<u64> {
    LOGS_EMITTED_COUNTER.get_or_init(|| {
        global::meter(SERVICE_NAME)
            .u64_counter("dataplane_logs_emitted_total")
            .with_description("Cumulative structured records submitted to the log writer")
            .init()
    })
}

fn record_counter_delta(
    counter: &Counter<u64>,
    last_recorded: &AtomicU64,
    current: u64,
    attributes: &[KeyValue],
) {
    let previous = last_recorded.swap(current, Ordering::AcqRel);
    let delta = current.saturating_sub(previous);
    if delta > 0 {
        counter.add(delta, attributes);
    }
}

fn parse_bounded_env<T>(name: &str, default: T, minimum: T, maximum: T) -> T
where
    T: std::str::FromStr + Ord + Copy,
{
    std::env::var(name)
        .ok()
        .and_then(|value| value.parse::<T>().ok())
        .unwrap_or(default)
        .clamp(minimum, maximum)
}

#[cfg(test)]
mod tests {
    use super::{
        bounded_field, is_stable_error_code, protected_field, sanitize_for_durable_event,
        LogFields, Logger,
    };
    use std::io::{self, Write};
    use std::sync::{Arc, Mutex};
    use tracing_subscriber::fmt::MakeWriter;

    #[derive(Clone, Default)]
    struct Capture(Arc<Mutex<Vec<u8>>>);

    struct CaptureWriter(Capture);

    impl Write for Capture {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    impl Write for CaptureWriter {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            self.0 .0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    impl<'writer> MakeWriter<'writer> for Capture {
        type Writer = CaptureWriter;

        fn make_writer(&'writer self) -> Self::Writer {
            CaptureWriter(self.clone())
        }
    }

    #[test]
    fn subscriber_emits_parseable_json_and_escapes_untrusted_fields() {
        let capture = Capture::default();
        let subscriber = tracing_subscriber::fmt()
            .json()
            .flatten_event(true)
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .with_writer(capture.clone())
            .finish();

        tracing::subscriber::with_default(subscriber, || {
            Logger::sys_info_with_fields(
                "logger.test",
                "LOGGER_JSON_ESCAPE_TEST",
                "quote=\" newline=\n slash=\\ unicode=Tiếng Việt",
                LogFields::default(),
            );
        });

        let bytes = capture.0.lock().unwrap().clone();
        let line = std::str::from_utf8(&bytes).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(line.trim()).unwrap();
        assert_eq!(
            parsed["message"],
            "quote=\" newline=\n slash=\\ unicode=Tiếng Việt"
        );
        assert_eq!(parsed["level"], "info");
        assert_eq!(line.matches("\"level\"").count(), 1);
        assert_eq!(parsed["event_code"], "LOGGER_JSON_ESCAPE_TEST");
        assert!(parsed["service_instance_id"]
            .as_str()
            .is_some_and(|value| !value.is_empty()));
    }

    #[test]
    fn bounds_utf8_fields_without_splitting_code_points() {
        let value = "é".repeat(20_000);
        let bounded = bounded_field(&value);
        assert!(bounded.len() < value.len());
        assert!(bounded.ends_with("...[truncated]"));
    }

    #[test]
    fn distinguishes_codes_from_raw_error_details() {
        assert!(is_stable_error_code("KAFKA_POLL_FAILED"));
        assert!(is_stable_error_code("otel_init_error"));
        assert!(!is_stable_error_code(
            "connection failed: broker unavailable"
        ));
    }

    #[test]
    fn redacts_known_secret_markers_before_serialization() {
        assert_eq!(
            protected_field("request failed Authorization: Bearer top-secret"),
            "[REDACTED_SENSITIVE_LOG_FIELD]"
        );
        assert_eq!(
            protected_field("PVEAPIToken=operator!runtime=secret-value"),
            "[REDACTED_SENSITIVE_LOG_FIELD]"
        );
        assert_eq!(
            protected_field("leader fencing_token=42"),
            "leader fencing_token=42"
        );
    }

    #[test]
    fn durable_event_fields_are_redacted_and_utf8_bounded() {
        assert_eq!(
            sanitize_for_durable_event("upstream password=do-not-publish", 4_096),
            "[REDACTED_SENSITIVE_EVENT_FIELD]"
        );
        let bounded = sanitize_for_durable_event(&"ế".repeat(4_096), 128);
        assert!(bounded.len() <= 128);
        assert!(bounded.ends_with("...[truncated]"));
    }

    #[test]
    fn non_blocking_writer_serializes_concurrent_events_without_interleaving() {
        let capture = Capture::default();
        let (writer, guard) = tracing_appender::non_blocking::NonBlockingBuilder::default()
            .buffered_lines_limit(4_096)
            .lossy(false)
            .finish(capture.clone());
        let subscriber = tracing_subscriber::fmt()
            .json()
            .flatten_event(true)
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .with_writer(writer)
            .finish();
        let dispatch = tracing::Dispatch::new(subscriber);

        std::thread::scope(|scope| {
            for worker in 0..8 {
                let dispatch = dispatch.clone();
                scope.spawn(move || {
                    tracing::dispatcher::with_default(&dispatch, || {
                        for index in 0..250 {
                            tracing::info!(
                                level = "info",
                                event_code = "LOGGER_CONCURRENCY_TEST",
                                worker,
                                index,
                                message = "concurrent record"
                            );
                        }
                    });
                });
            }
        });
        drop(guard);

        let bytes = capture.0.lock().unwrap().clone();
        let output = std::str::from_utf8(&bytes).unwrap();
        let lines = output.lines().collect::<Vec<_>>();
        assert_eq!(lines.len(), 2_000);
        for line in lines {
            let parsed: serde_json::Value = serde_json::from_str(line).unwrap();
            assert_eq!(parsed["event_code"], "LOGGER_CONCURRENCY_TEST");
        }
    }

    #[test]
    fn non_blocking_queue_reports_overflow_instead_of_blocking_producers() {
        struct SlowSink;

        impl Write for SlowSink {
            fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
                std::thread::sleep(std::time::Duration::from_millis(10));
                Ok(bytes.len())
            }

            fn flush(&mut self) -> io::Result<()> {
                Ok(())
            }
        }

        let (writer, guard) = tracing_appender::non_blocking::NonBlockingBuilder::default()
            .buffered_lines_limit(1)
            .lossy(true)
            .finish(SlowSink);
        let error_counter = writer.error_counter();
        let subscriber = tracing_subscriber::fmt()
            .json()
            .with_ansi(false)
            .with_writer(writer)
            .finish();

        tracing::subscriber::with_default(subscriber, || {
            for index in 0..1_000 {
                tracing::info!(index, "overflow test");
            }
        });
        drop(guard);
        assert!(error_counter.dropped_lines() > 0);
    }
}
