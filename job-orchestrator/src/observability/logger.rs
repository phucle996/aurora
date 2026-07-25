use std::borrow::Cow;
use std::collections::HashMap;
use std::hash::{DefaultHasher, Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};

use opentelemetry::global;
use opentelemetry::metrics::Counter;
use tracing_appender::non_blocking::{ErrorCounter, NonBlockingBuilder, WorkerGuard};
use tracing_subscriber::EnvFilter;

pub const LOG_TYPE_SYSTEM: &str = "system";
pub const LOG_TYPE_JOB: &str = "job";

const SERVICE_NAME: &str = "aurora-job-orchestrator";
const DEFAULT_MAX_FIELD_BYTES: usize = 16 * 1024;
const DEFAULT_BUFFERED_LINES: usize = 16 * 1024;
const DEFAULT_RATE_LIMIT_MS: u64 = 5_000;
const MAX_RATE_LIMIT_KEYS: usize = 2_048;

static SETTINGS: OnceLock<LoggerSettings> = OnceLock::new();
static CONTEXT: OnceLock<LoggerContext> = OnceLock::new();
static PROCESS_SEQUENCE: AtomicU64 = AtomicU64::new(0);
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
    deployment_environment: String,
    node_id: String,
    boot_id: String,
    pid: u32,
}

impl LoggerContext {
    fn load() -> Self {
        let node_id = crate::config::get_node_hostname();
        Self {
            deployment_environment: std::env::var("DEPLOYMENT_ENVIRONMENT")
                .or_else(|_| std::env::var("APP_ENV"))
                .unwrap_or_else(|_| "unknown".to_string()),
            node_id: if node_id.is_empty() {
                format!("job-orchestrator-{}", std::process::id())
            } else {
                node_id
            },
            // boot_id + process_sequence identifies one physical emission. It must remain
            // separate from event_id, which is stable across replay and pod failover.
            boot_id: uuid::Uuid::new_v4().to_string(),
            pid: std::process::id(),
        }
    }
}

struct RateLimitEntry {
    last_emitted: Instant,
    suppressed: u64,
}

/// Typed context for transport and durability-boundary logs. Empty values remain explicit so
/// collectors never need to parse a human message to recover Kafka coordinates or outcomes.
#[derive(Clone, Copy, Default)]
pub struct LogFields<'a> {
    pub event_id: Option<&'a str>,
    pub operation_id: Option<&'a str>,
    pub source_domain: Option<&'a str>,
    pub job_version: Option<u64>,
    pub kafka_topic: Option<&'a str>,
    pub kafka_partition: Option<i32>,
    pub kafka_offset: Option<i64>,
    pub retryable: Option<bool>,
    pub duration_ms: Option<u64>,
    pub outcome: Option<&'a str>,
}

/// Owns the asynchronous stdout writer. Normal bootstrap errors and SIGTERM paths unwind through
/// this guard, queue a final summary, and join the writer before the process exits.
#[must_use = "dropping LoggerGuard immediately stops the asynchronous log writer"]
pub struct LoggerGuard {
    worker_guard: Option<WorkerGuard>,
}

impl Drop for LoggerGuard {
    fn drop(&mut self) {
        if self.worker_guard.is_some() {
            Logger::emit_shutdown_summary();
        }
        self.worker_guard.take();
    }
}

pub struct Logger;

impl Logger {
    /// Installs one real NDJSON serializer backed by a bounded, lossy queue.
    ///
    /// Log loss under collector backpressure is observable. Blocking a Tokio executor thread is
    /// forbidden because Kafka/PostgreSQL durability, not diagnostic output, is the correctness
    /// boundary.
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
            .thread_name("aurora-job-orchestrator-log-writer")
            .finish(std::io::stdout());
        let _ = APPENDER_ERROR_COUNTER.set(writer.error_counter());

        let filter = EnvFilter::try_new(format!("{SERVICE_NAME}={}", settings.level.as_str()))
            .expect("static Job Orchestrator log filter must be valid");
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
            .expect("Job Orchestrator structured logger must be initialized exactly once");

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
                "Job Orchestrator log identity initialized for node={} boot={}",
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

    pub fn node_id() -> &'static str {
        &CONTEXT.get_or_init(LoggerContext::load).node_id
    }

    pub fn deployment_environment() -> &'static str {
        &CONTEXT
            .get_or_init(LoggerContext::load)
            .deployment_environment
    }

    pub fn dropped_lines() -> u64 {
        APPENDER_ERROR_COUNTER
            .get()
            .map(|counter| counter.dropped_lines() as u64)
            .unwrap_or_default()
    }

    /// Exports process-local cumulative logger health as deltas. The resource instance ID is the
    /// same boot_id used by logs, so a pod restart cannot be mistaken for a counter rollback.
    pub fn record_pipeline_metrics() {
        record_counter_delta(
            logs_dropped_counter(),
            &LAST_RECORDED_DROPPED,
            Self::dropped_lines(),
        );
        record_counter_delta(
            logs_suppressed_counter(),
            &LAST_RECORDED_SUPPRESSED,
            SUPPRESSED_LINES.load(Ordering::Relaxed),
        );
        record_counter_delta(
            logs_emitted_counter(),
            &LAST_RECORDED_EMITTED,
            EMITTED_LINES.load(Ordering::Relaxed),
        );
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
            Self::emit(
                LogLevel::Info,
                LOG_TYPE_SYSTEM,
                op,
                event_code,
                message,
                "",
                fields,
                false,
            );
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
            Self::emit(
                LogLevel::Warn,
                LOG_TYPE_SYSTEM,
                op,
                event_code,
                message,
                error,
                fields,
                true,
            );
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
                LOG_TYPE_SYSTEM,
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
        Self::job_log_with_fields(
            job_id,
            job_topic,
            attempt,
            op,
            "JOB_LIFECYCLE_EVENT",
            message,
            LogFields::default(),
        );
    }

    #[allow(clippy::too_many_arguments)]
    pub fn job_log_with_fields<'a>(
        job_id: &'a str,
        job_topic: &str,
        attempt: u32,
        op: &str,
        event_code: &str,
        message: &str,
        mut fields: LogFields<'a>,
    ) {
        if Self::get_level() <= LogLevel::Info {
            fields.operation_id.get_or_insert(job_id);
            fields.outcome.get_or_insert("observed");
            Self::emit_job(job_id, job_topic, attempt, op, event_code, message, fields);
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn emit(
        level: LogLevel,
        log_type: &str,
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
        let sequence = PROCESS_SEQUENCE.fetch_add(1, Ordering::Relaxed) + 1;
        let trace_id =
            crate::observability::otel::OtelTracer::get_current_trace_id().unwrap_or_default();
        let span_id =
            crate::observability::otel::OtelTracer::get_current_span_id().unwrap_or_default();
        let event_id = protected_field(fields.event_id.unwrap_or_default());
        let operation_id = protected_field(fields.operation_id.unwrap_or_default());
        let source_domain = bounded_field(fields.source_domain.unwrap_or_default());
        let kafka_topic = protected_field(fields.kafka_topic.unwrap_or_default());
        let outcome = bounded_field(fields.outcome.unwrap_or_default());

        macro_rules! emit_at {
            ($tracing_level:expr) => {
                tracing::event!(
                    target: SERVICE_NAME,
                    $tracing_level,
                    level = level.as_str(),
                    service_name = SERVICE_NAME,
                    service_version = env!("CARGO_PKG_VERSION"),
                    deployment_environment = context.deployment_environment.as_str(),
                    component_scope = "central",
                    node_id = context.node_id.as_str(),
                    boot_id = context.boot_id.as_str(),
                    pid = context.pid,
                    process_sequence = sequence,
                    log_type,
                    op = op.as_ref(),
                    event_code = event_code.as_ref(),
                    event_id = event_id.as_ref(),
                    operation_id = operation_id.as_ref(),
                    trace_id = trace_id.as_str(),
                    span_id = span_id.as_str(),
                    source_domain = source_domain.as_ref(),
                    job_version = fields.job_version.unwrap_or_default(),
                    kafka_topic = kafka_topic.as_ref(),
                    kafka_partition = fields.kafka_partition.unwrap_or(-1),
                    kafka_offset = fields.kafka_offset.unwrap_or(-1),
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
        let sequence = PROCESS_SEQUENCE.fetch_add(1, Ordering::Relaxed) + 1;
        let trace_id =
            crate::observability::otel::OtelTracer::get_current_trace_id().unwrap_or_default();
        let span_id =
            crate::observability::otel::OtelTracer::get_current_span_id().unwrap_or_default();
        let message = protected_field(message);
        let job_id = protected_field(job_id);
        let job_topic = protected_field(job_topic);
        let op = bounded_field(op);
        let event_id = protected_field(fields.event_id.unwrap_or_default());
        let operation_id = protected_field(fields.operation_id.unwrap_or(job_id.as_ref()));
        let source_domain = bounded_field(fields.source_domain.unwrap_or_default());
        let kafka_topic = protected_field(fields.kafka_topic.unwrap_or_default());
        let outcome = bounded_field(fields.outcome.unwrap_or_default());

        tracing::event!(
            target: SERVICE_NAME,
            tracing::Level::INFO,
            level = "info",
            service_name = SERVICE_NAME,
            service_version = env!("CARGO_PKG_VERSION"),
            deployment_environment = context.deployment_environment.as_str(),
            component_scope = "central",
            node_id = context.node_id.as_str(),
            boot_id = context.boot_id.as_str(),
            pid = context.pid,
            process_sequence = sequence,
            log_type = LOG_TYPE_JOB,
            op = op.as_ref(),
            event_code,
            event_id = event_id.as_ref(),
            operation_id = operation_id.as_ref(),
            trace_id = trace_id.as_str(),
            span_id = span_id.as_str(),
            source_domain = source_domain.as_ref(),
            job_id = job_id.as_ref(),
            job_topic = job_topic.as_ref(),
            job_version = fields.job_version.unwrap_or_default(),
            attempt,
            kafka_topic = kafka_topic.as_ref(),
            kafka_partition = fields.kafka_partition.unwrap_or(-1),
            kafka_offset = fields.kafka_offset.unwrap_or(-1),
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
    const SUFFIX: &str = "...[truncated]";
    let content_limit = max_bytes.saturating_sub(SUFFIX.len());
    let mut boundary = content_limit;
    while boundary > 0 && !value.is_char_boundary(boundary) {
        boundary -= 1;
    }
    Cow::Owned(format!("{}{SUFFIX}", &value[..boundary]))
}

fn protected_field(value: &str) -> Cow<'_, str> {
    let bounded = bounded_field(value);
    if contains_sensitive_marker(bounded.as_ref()) || contains_embedded_url_credentials(&bounded) {
        Cow::Borrowed("[REDACTED_SENSITIVE_LOG_FIELD]")
    } else {
        bounded
    }
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
        b"token:",
        b"token=",
        b"kafka_password:",
        b"kafka_password=",
        b"x-amz-security-token",
    ];
    let bytes = value.as_bytes();
    MARKERS.iter().any(|marker| {
        bytes
            .windows(marker.len())
            .any(|window| window.eq_ignore_ascii_case(marker))
    })
}

fn contains_embedded_url_credentials(value: &str) -> bool {
    value.split_ascii_whitespace().any(|token| {
        let Some(scheme_end) = token.find("://") else {
            return false;
        };
        let authority = token[scheme_end + 3..]
            .split(['/', '?', '#'])
            .next()
            .unwrap_or_default();
        authority.contains('@')
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

    // Untrusted, high-cardinality errors must not grow the suppression table without a bound.
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
            .u64_counter("job_orchestrator_logs_dropped_total")
            .with_description("Log records dropped by the bounded non-blocking writer")
            .init()
    })
}

fn logs_suppressed_counter() -> &'static Counter<u64> {
    LOGS_SUPPRESSED_COUNTER.get_or_init(|| {
        global::meter(SERVICE_NAME)
            .u64_counter("job_orchestrator_logs_suppressed_total")
            .with_description("Duplicate warning and error records suppressed by rate limiting")
            .init()
    })
}

fn logs_emitted_counter() -> &'static Counter<u64> {
    LOGS_EMITTED_COUNTER.get_or_init(|| {
        global::meter(SERVICE_NAME)
            .u64_counter("job_orchestrator_logs_emitted_total")
            .with_description("Structured records submitted to the asynchronous log writer")
            .init()
    })
}

fn record_counter_delta(counter: &Counter<u64>, last_recorded: &AtomicU64, current: u64) {
    let previous = last_recorded.swap(current, Ordering::AcqRel);
    let delta = current.saturating_sub(previous);
    if delta > 0 {
        counter.add(delta, &[]);
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
    use super::{bounded_field, protected_field, LogFields, Logger, DEFAULT_MAX_FIELD_BYTES};
    use std::io::{self, Write};
    use std::sync::{Arc, Mutex};
    use tracing_appender::non_blocking::NonBlockingBuilder;
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
    }

    #[test]
    fn bounds_utf8_fields_without_splitting_code_points() {
        let value = "é".repeat(20_000);
        let bounded = bounded_field(&value);
        assert!(bounded.len() < value.len());
        assert!(bounded.len() <= DEFAULT_MAX_FIELD_BYTES);
        assert!(bounded.ends_with("...[truncated]"));
    }

    #[test]
    fn redacts_markers_and_urls_with_embedded_credentials() {
        assert_eq!(
            protected_field("request failed Authorization: Bearer top-secret"),
            "[REDACTED_SENSITIVE_LOG_FIELD]"
        );
        assert_eq!(
            protected_field("redis://runtime:password@shared-redis:6379"),
            "[REDACTED_SENSITIVE_LOG_FIELD]"
        );
        assert_eq!(
            protected_field("broker connection temporarily unavailable"),
            "broker connection temporarily unavailable"
        );
        assert_eq!(
            protected_field("DATABASE_URL must be set"),
            "DATABASE_URL must be set"
        );
    }

    #[test]
    fn non_blocking_writer_serializes_concurrent_events_without_interleaving() {
        let capture = Capture::default();
        let (writer, guard) = NonBlockingBuilder::default()
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
                        for index in 0..100 {
                            tracing::info!(
                                level = "info",
                                event_code = "LOGGER_CONCURRENCY_TEST",
                                worker,
                                index,
                            );
                        }
                    });
                });
            }
        });
        drop(guard);

        let bytes = capture.0.lock().unwrap().clone();
        let lines = std::str::from_utf8(&bytes)
            .unwrap()
            .lines()
            .collect::<Vec<_>>();
        assert_eq!(lines.len(), 800);
        assert!(lines
            .iter()
            .all(|line| serde_json::from_str::<serde_json::Value>(line).is_ok()));
    }
}
