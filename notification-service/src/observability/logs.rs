use std::borrow::Cow;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::OnceLock;
use std::time::Instant;
use tracing::Level;

const RATE_LIMIT_SLOTS: usize = 1024;
const RATE_LIMIT_INDEX_MASK: u64 = (RATE_LIMIT_SLOTS as u64) - 1;
const RATE_LIMIT_TIMESTAMP_MASK: u64 = (1_u64 << 48) - 1;
const DEFAULT_MAX_FIELD_BYTES: usize = 16 * 1024;
const DEFAULT_WARN_RATE_LIMIT_MS: u64 = 1_000;

static IDENTITY: OnceLock<LogIdentity> = OnceLock::new();
static STARTED_AT: OnceLock<Instant> = OnceLock::new();
static MAX_FIELD_BYTES: OnceLock<usize> = OnceLock::new();
static WARN_RATE_LIMIT_MS: OnceLock<u64> = OnceLock::new();
static EVENT_ATTEMPTS: AtomicU64 = AtomicU64::new(0);
static SUPPRESSED_EVENTS: AtomicU64 = AtomicU64::new(0);
static RATE_LIMITER: [AtomicU64; RATE_LIMIT_SLOTS] =
    [const { AtomicU64::new(0) }; RATE_LIMIT_SLOTS];

#[derive(Debug)]
pub(crate) struct LogIdentity {
    pub boot_id: String,
    pub service_version: String,
}

#[derive(Debug, Clone, Copy)]
pub(crate) struct LogStats {
    pub attempts: u64,
    pub suppressed: u64,
}

pub(crate) fn install_identity(identity: LogIdentity) -> Result<(), LogIdentity> {
    IDENTITY.set(identity)
}

pub(crate) fn stats() -> LogStats {
    LogStats {
        attempts: EVENT_ATTEMPTS.load(Ordering::Relaxed),
        suppressed: SUPPRESSED_EVENTS.load(Ordering::Relaxed),
    }
}

fn identity() -> Option<&'static LogIdentity> {
    IDENTITY.get()
}

fn max_field_bytes() -> usize {
    *MAX_FIELD_BYTES.get_or_init(|| {
        env_usize(
            "APP_LOG_MAX_FIELD_BYTES",
            DEFAULT_MAX_FIELD_BYTES,
            256,
            256 * 1024,
        )
    })
}

fn bounded(value: &str) -> Cow<'_, str> {
    let limit = max_field_bytes();
    if value.len() <= limit {
        return Cow::Borrowed(value);
    }

    // Preserve UTF-8 while bounding the only per-event heap allocation under
    // attacker-controlled or unexpectedly large fields.
    let mut end = limit.min(value.len());
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    Cow::Owned(format!("{}...[truncated]", &value[..end]))
}

fn warn_rate_limit_ms() -> u64 {
    *WARN_RATE_LIMIT_MS.get_or_init(|| {
        env_u64(
            "APP_LOG_WARN_RATE_LIMIT_MS",
            DEFAULT_WARN_RATE_LIMIT_MS,
            0,
            60_000,
        )
    })
}

fn should_emit_cold_path(level: Level, operation: &str) -> bool {
    let interval_ms = warn_rate_limit_ms();
    should_emit_cold_path_with_interval(level, operation, interval_ms)
}

fn should_emit_cold_path_with_interval(level: Level, operation: &str, interval_ms: u64) -> bool {
    if interval_ms == 0 {
        return true;
    }

    let hash = fnv1a64(level.as_str().as_bytes()).wrapping_mul(0x100000001b3)
        ^ fnv1a64(operation.as_bytes());
    let slot = &RATE_LIMITER[(hash & RATE_LIMIT_INDEX_MASK) as usize];
    let tag = (((hash >> 16) as u16) | 1) as u64;
    let now = STARTED_AT
        .get_or_init(Instant::now)
        .elapsed()
        .as_millis()
        .min(RATE_LIMIT_TIMESTAMP_MASK as u128) as u64
        + 1;

    loop {
        let current = slot.load(Ordering::Relaxed);
        let current_tag = current >> 48;
        let last = current & RATE_LIMIT_TIMESTAMP_MASK;
        if current_tag == tag && now.saturating_sub(last) < interval_ms {
            SUPPRESSED_EVENTS.fetch_add(1, Ordering::Relaxed);
            return false;
        }

        let next = (tag << 48) | (now & RATE_LIMIT_TIMESTAMP_MASK);
        // A collision is allowed to replace the slot. The embedded tag keeps
        // unrelated operations from suppressing one another except on a full
        // 16-bit hash collision; no mutex or unbounded map touches the path.
        if slot
            .compare_exchange_weak(current, next, Ordering::Relaxed, Ordering::Relaxed)
            .is_ok()
        {
            return true;
        }
    }
}

fn fnv1a64(bytes: &[u8]) -> u64 {
    let mut hash = 0xcbf29ce484222325_u64;
    for byte in bytes {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x100000001b3);
    }
    hash
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

pub struct Logger;

impl Logger {
    pub fn access_log(
        operation: &str,
        method: &str,
        route: &str,
        status_code: i32,
        latency_ms: f64,
    ) {
        if !tracing::enabled!(Level::INFO) {
            return;
        }
        EVENT_ATTEMPTS.fetch_add(1, Ordering::Relaxed);
        let (trace_id, span_id) = super::traces::current_span_identifiers();
        let identity = identity();
        tracing::event!(
            Level::INFO,
            event_code = "HTTP_ACCESS",
            severity_text = "INFO",
            severity_number = 9,
            op = %bounded(operation),
            http_request_method = %bounded(method),
            url_route = %bounded(route),
            http_response_status_code = status_code,
            duration_ms = latency_ms,
            trace_id = trace_id.as_deref().unwrap_or(""),
            span_id = span_id.as_deref().unwrap_or(""),
            service_name = super::SERVICE_NAME,
            service_version = identity.map(|value| value.service_version.as_str()).unwrap_or(env!("CARGO_PKG_VERSION")),
            service_instance_id = identity.map(|value| value.boot_id.as_str()).unwrap_or(""),
        );
    }

    pub fn sys_info(operation: &str, message: &str) {
        if !tracing::enabled!(Level::INFO) {
            return;
        }
        EVENT_ATTEMPTS.fetch_add(1, Ordering::Relaxed);
        emit_system(Level::INFO, operation, message, "");
    }

    pub fn sys_warn(operation: &str, message: &str, error: &str) {
        if !tracing::enabled!(Level::WARN) || !should_emit_cold_path(Level::WARN, operation) {
            return;
        }
        EVENT_ATTEMPTS.fetch_add(1, Ordering::Relaxed);
        emit_system(Level::WARN, operation, message, error);
    }

    pub fn sys_error(operation: &str, message: &str, error: &str) {
        if !tracing::enabled!(Level::ERROR) || !should_emit_cold_path(Level::ERROR, operation) {
            return;
        }
        EVENT_ATTEMPTS.fetch_add(1, Ordering::Relaxed);
        emit_system(Level::ERROR, operation, message, error);
    }
}

fn emit_system(level: Level, operation: &str, message: &str, error: &str) {
    let (trace_id, span_id) = super::traces::current_span_identifiers();
    let identity = identity();
    let operation = bounded(operation);
    let message = bounded(message);
    let error = bounded(error);
    let instance_id = identity.map(|value| value.boot_id.as_str()).unwrap_or("");
    let service_version = identity
        .map(|value| value.service_version.as_str())
        .unwrap_or(env!("CARGO_PKG_VERSION"));
    let (event_code, severity_text, severity_number) = match level {
        Level::ERROR => ("SYSTEM_ERROR", "ERROR", 17),
        Level::WARN => ("SYSTEM_WARNING", "WARN", 13),
        _ => ("SYSTEM_INFO", "INFO", 9),
    };

    match level {
        Level::ERROR => tracing::event!(
            Level::ERROR,
            event_code,
            severity_text,
            severity_number,
            op = %operation,
            message = %message,
            error_cause = %error,
            trace_id = trace_id.as_deref().unwrap_or(""),
            span_id = span_id.as_deref().unwrap_or(""),
            service_name = super::SERVICE_NAME,
            service_version,
            service_instance_id = instance_id,
        ),
        Level::WARN => tracing::event!(
            Level::WARN,
            event_code,
            severity_text,
            severity_number,
            op = %operation,
            message = %message,
            error_cause = %error,
            trace_id = trace_id.as_deref().unwrap_or(""),
            span_id = span_id.as_deref().unwrap_or(""),
            service_name = super::SERVICE_NAME,
            service_version,
            service_instance_id = instance_id,
        ),
        _ => tracing::event!(
            Level::INFO,
            event_code,
            severity_text,
            severity_number,
            op = %operation,
            message = %message,
            trace_id = trace_id.as_deref().unwrap_or(""),
            span_id = span_id.as_deref().unwrap_or(""),
            service_name = super::SERVICE_NAME,
            service_version,
            service_instance_id = instance_id,
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::{bounded, should_emit_cold_path_with_interval, Logger};
    use std::io::Write;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::{Arc, Barrier};
    use std::time::{Duration, Instant};
    use tracing::Level;

    #[test]
    fn bounded_field_keeps_utf8_valid() {
        let value = "ư".repeat(200_000);
        let bounded = bounded(&value);
        assert!(bounded.is_char_boundary(bounded.len()));
        assert!(bounded.ends_with("...[truncated]"));
    }

    #[test]
    fn cold_path_rate_limit_is_atomic_under_contention() {
        const THREADS: usize = 16;
        let barrier = Arc::new(Barrier::new(THREADS));
        let emitted = Arc::new(AtomicUsize::new(0));
        let mut handles = Vec::with_capacity(THREADS);

        for _ in 0..THREADS {
            let barrier = Arc::clone(&barrier);
            let emitted = Arc::clone(&emitted);
            handles.push(std::thread::spawn(move || {
                barrier.wait();
                if should_emit_cold_path_with_interval(
                    Level::ERROR,
                    "test.atomic_rate_limit.unique",
                    60_000,
                ) {
                    emitted.fetch_add(1, Ordering::Relaxed);
                }
            }));
        }
        for handle in handles {
            handle.join().expect("rate limit worker panicked");
        }

        assert_eq!(emitted.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn saturated_log_writer_never_backpressures_caller() {
        struct SlowWriter;

        impl Write for SlowWriter {
            fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
                std::thread::sleep(Duration::from_millis(5));
                Ok(bytes.len())
            }

            fn flush(&mut self) -> std::io::Result<()> {
                Ok(())
            }
        }

        let (writer, guard) = tracing_appender::non_blocking::NonBlockingBuilder::default()
            .buffered_lines_limit(16)
            .lossy(true)
            .finish(SlowWriter);
        let dropped = writer.error_counter();
        let subscriber = tracing_subscriber::fmt()
            .json()
            .with_writer(writer)
            .finish();
        let started = Instant::now();

        tracing::subscriber::with_default(subscriber, || {
            for _ in 0..10_000 {
                Logger::sys_info("test.log_overload", "bounded queue overload probe");
            }
        });

        assert!(
            started.elapsed() < Duration::from_secs(5),
            "lossy logger unexpectedly backpressured its caller"
        );
        assert!(dropped.dropped_lines() > 0);
        drop(guard);
    }
}
