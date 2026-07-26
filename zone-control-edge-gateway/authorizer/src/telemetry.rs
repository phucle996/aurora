use std::fmt::Write;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::{watch, Semaphore};

use crate::error::AuthzError;

const CHECK_DURATION_BUCKETS_MS: [u64; 10] = [1, 2, 5, 10, 25, 50, 100, 250, 500, 1_000];

#[derive(Default)]
pub struct Telemetry {
    allowed: AtomicU64,
    denied: AtomicU64,
    not_ready: AtomicU64,
    dependency_failure: AtomicU64,
    overloaded: AtomicU64,
    cache_hit: AtomicU64,
    cache_miss: AtomicU64,
    kv_read_failure: AtomicU64,
    watch_restart: AtomicU64,
    inflight: AtomicU64,
    check_duration_count: AtomicU64,
    check_duration_micros: AtomicU64,
    check_duration_buckets: [AtomicU64; CHECK_DURATION_BUCKETS_MS.len()],
    ready: AtomicBool,
}

impl Telemetry {
    pub fn observe_check(&self) -> CheckObservation<'_> {
        self.inflight.fetch_add(1, Ordering::Relaxed);
        CheckObservation {
            telemetry: self,
            started: Instant::now(),
        }
    }

    pub fn allowed(&self) {
        self.allowed.fetch_add(1, Ordering::Relaxed);
    }

    pub fn denied(&self) {
        self.denied.fetch_add(1, Ordering::Relaxed);
    }

    pub fn not_ready(&self) {
        self.not_ready.fetch_add(1, Ordering::Relaxed);
    }

    pub fn dependency_failure(&self) {
        self.dependency_failure.fetch_add(1, Ordering::Relaxed);
    }

    pub fn overloaded(&self) {
        self.overloaded.fetch_add(1, Ordering::Relaxed);
    }

    pub fn cache_hit(&self) {
        self.cache_hit.fetch_add(1, Ordering::Relaxed);
    }

    pub fn cache_miss(&self) {
        self.cache_miss.fetch_add(1, Ordering::Relaxed);
    }

    pub fn kv_read_failure(&self) {
        self.kv_read_failure.fetch_add(1, Ordering::Relaxed);
    }

    pub fn watch_restart(&self) {
        self.watch_restart.fetch_add(1, Ordering::Relaxed);
    }

    pub fn set_ready(&self, ready: bool) {
        self.ready.store(ready, Ordering::Release);
    }

    pub fn is_ready(&self) -> bool {
        self.ready.load(Ordering::Acquire)
    }

    fn prometheus(&self) -> String {
        let mut output = format!(
            concat!(
                "# TYPE aurora_zone_control_authorizer_checks_total counter\n",
                "aurora_zone_control_authorizer_checks_total{{outcome=\"allowed\"}} {}\n",
                "aurora_zone_control_authorizer_checks_total{{outcome=\"denied\"}} {}\n",
                "aurora_zone_control_authorizer_checks_total{{outcome=\"not_ready\"}} {}\n",
                "aurora_zone_control_authorizer_checks_total{{outcome=\"dependency_failure\"}} {}\n",
                "aurora_zone_control_authorizer_checks_total{{outcome=\"overloaded\"}} {}\n",
                "# TYPE aurora_zone_control_authorizer_cache_total counter\n",
                "aurora_zone_control_authorizer_cache_total{{outcome=\"hit\"}} {}\n",
                "aurora_zone_control_authorizer_cache_total{{outcome=\"miss\"}} {}\n",
                "# TYPE aurora_zone_control_authorizer_kv_read_failures_total counter\n",
                "aurora_zone_control_authorizer_kv_read_failures_total {}\n",
                "# TYPE aurora_zone_control_authorizer_watch_restarts_total counter\n",
                "aurora_zone_control_authorizer_watch_restarts_total {}\n",
                "# TYPE aurora_zone_control_authorizer_inflight gauge\n",
                "aurora_zone_control_authorizer_inflight {}\n",
                "# TYPE aurora_zone_control_authorizer_ready gauge\n",
                "aurora_zone_control_authorizer_ready {}\n",
                "# TYPE aurora_zone_control_authorizer_check_duration_seconds histogram\n"
            ),
            self.allowed.load(Ordering::Relaxed),
            self.denied.load(Ordering::Relaxed),
            self.not_ready.load(Ordering::Relaxed),
            self.dependency_failure.load(Ordering::Relaxed),
            self.overloaded.load(Ordering::Relaxed),
            self.cache_hit.load(Ordering::Relaxed),
            self.cache_miss.load(Ordering::Relaxed),
            self.kv_read_failure.load(Ordering::Relaxed),
            self.watch_restart.load(Ordering::Relaxed),
            self.inflight.load(Ordering::Relaxed),
            u8::from(self.is_ready()),
        );
        for (index, bound_ms) in CHECK_DURATION_BUCKETS_MS.iter().enumerate() {
            let _ = writeln!(
                output,
                "aurora_zone_control_authorizer_check_duration_seconds_bucket{{le=\"{}\"}} {}",
                (*bound_ms as f64) / 1_000.0,
                self.check_duration_buckets[index].load(Ordering::Relaxed)
            );
        }
        let count = self.check_duration_count.load(Ordering::Relaxed);
        let _ = writeln!(
            output,
            "aurora_zone_control_authorizer_check_duration_seconds_bucket{{le=\"+Inf\"}} {count}"
        );
        let _ = writeln!(
            output,
            "aurora_zone_control_authorizer_check_duration_seconds_sum {}",
            (self.check_duration_micros.load(Ordering::Relaxed) as f64) / 1_000_000.0
        );
        let _ = writeln!(
            output,
            "aurora_zone_control_authorizer_check_duration_seconds_count {count}"
        );
        output
    }
}

pub struct CheckObservation<'a> {
    telemetry: &'a Telemetry,
    started: Instant,
}

impl Drop for CheckObservation<'_> {
    fn drop(&mut self) {
        let elapsed = self.started.elapsed();
        let micros = elapsed.as_micros().min(u128::from(u64::MAX)) as u64;
        self.telemetry.inflight.fetch_sub(1, Ordering::Relaxed);
        self.telemetry
            .check_duration_count
            .fetch_add(1, Ordering::Relaxed);
        self.telemetry
            .check_duration_micros
            .fetch_add(micros, Ordering::Relaxed);
        for (index, bound_ms) in CHECK_DURATION_BUCKETS_MS.iter().enumerate() {
            if micros <= bound_ms.saturating_mul(1_000) {
                self.telemetry.check_duration_buckets[index].fetch_add(1, Ordering::Relaxed);
            }
        }
    }
}

pub async fn bind(address: SocketAddr) -> Result<TcpListener, AuthzError> {
    TcpListener::bind(address)
        .await
        .map_err(|error| AuthzError::Dependency(format!("bind telemetry server failed: {error}")))
}

pub async fn serve(
    listener: TcpListener,
    telemetry: Arc<Telemetry>,
    mut shutdown: watch::Receiver<bool>,
) -> Result<(), AuthzError> {
    let permits = Arc::new(Semaphore::new(32));
    loop {
        tokio::select! {
            changed = shutdown.changed() => {
                if changed.is_err() || *shutdown.borrow() {
                    return Ok(());
                }
            }
            accepted = listener.accept() => {
                let (stream, _) = accepted.map_err(|error| {
                    AuthzError::Dependency(format!("telemetry accept failed: {error}"))
                })?;
                let Ok(permit) = permits.clone().try_acquire_owned() else {
                    continue;
                };
                let telemetry = telemetry.clone();
                tokio::spawn(async move {
                    let _permit = permit;
                    let _ = tokio::time::timeout(
                        Duration::from_secs(2),
                        respond(stream, telemetry),
                    )
                    .await;
                });
            }
        }
    }
}

async fn respond(mut stream: TcpStream, telemetry: Arc<Telemetry>) -> std::io::Result<()> {
    let mut request = [0_u8; 1024];
    let read = stream.read(&mut request).await?;
    let target = std::str::from_utf8(&request[..read])
        .ok()
        .and_then(|request| request.lines().next())
        .and_then(|line| line.split_whitespace().nth(1))
        .unwrap_or("");
    let (status, content_type, body) = match target {
        "/health/live" => ("200 OK", "text/plain", "ok\n".to_string()),
        "/health/ready" if telemetry.is_ready() => ("200 OK", "text/plain", "ready\n".to_string()),
        "/health/ready" => (
            "503 Service Unavailable",
            "text/plain",
            "not ready\n".to_string(),
        ),
        "/metrics" => (
            "200 OK",
            "text/plain; version=0.0.4",
            telemetry.prometheus(),
        ),
        _ => ("404 Not Found", "text/plain", "not found\n".to_string()),
    };
    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    stream.write_all(response.as_bytes()).await?;
    stream.shutdown().await
}

#[cfg(test)]
mod tests {
    use super::Telemetry;

    #[test]
    fn check_observation_closes_inflight_and_emits_histogram() {
        let telemetry = Telemetry::default();
        {
            let _observation = telemetry.observe_check();
            telemetry.allowed();
        }
        let output = telemetry.prometheus();
        assert!(output.contains("aurora_zone_control_authorizer_inflight 0"));
        assert!(output.contains("aurora_zone_control_authorizer_check_duration_seconds_count 1"));
        assert!(output.contains(
            "aurora_zone_control_authorizer_check_duration_seconds_bucket{le=\"+Inf\"} 1"
        ));
    }
}
