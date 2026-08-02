use std::{env, net::SocketAddr, time::Duration};

use thiserror::Error;
use uuid::Uuid;

const MAX_STREAM_LIFETIME_SECONDS: u64 = 300;
const MAX_SNAPSHOT_SECONDS: u64 = 300;
const MAX_EVENT_BYTES: usize = 4 * 1024 * 1024;
const MAX_LOG_LINES: usize = 1_000;

#[derive(Clone, Debug)]
pub struct Config {
    pub listen_addr: SocketAddr,
    pub zone_id: Uuid,
    pub victoria_metrics_url: String,
    pub victoria_logs_url: String,
    pub max_connections: usize,
    pub max_fanout_groups: usize,
    pub max_buffered_events: usize,
    pub max_lifetime: Duration,
    pub heartbeat: Duration,
    pub query_interval: Duration,
    pub max_snapshot: Duration,
    pub max_event_bytes: usize,
    pub max_log_lines: usize,
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("{0} is required")]
    Missing(&'static str),
    #[error("{0} is invalid")]
    Invalid(&'static str),
    #[error("{0} must be greater than zero")]
    Zero(&'static str),
    #[error("{0} exceeds the hard safety limit")]
    Limit(&'static str),
}

impl Config {
    pub fn from_env() -> Result<Self, ConfigError> {
        let listen_addr = required("RUNTIME_STREAM_LISTEN")?
            .parse()
            .map_err(|_| ConfigError::Invalid("RUNTIME_STREAM_LISTEN"))?;
        let zone_id =
            Uuid::parse_str(&required("ZONE_ID")?).map_err(|_| ConfigError::Invalid("ZONE_ID"))?;
        if zone_id.is_nil() {
            return Err(ConfigError::Invalid("ZONE_ID"));
        }
        let victoria_metrics_url = required("VICTORIA_METRICS_URL")?;
        let victoria_logs_url = required("VICTORIA_LOGS_URL")?;
        if !victoria_metrics_url.starts_with("http://")
            && !victoria_metrics_url.starts_with("https://")
        {
            return Err(ConfigError::Invalid("VICTORIA_METRICS_URL"));
        }
        if !victoria_logs_url.starts_with("http://") && !victoria_logs_url.starts_with("https://") {
            return Err(ConfigError::Invalid("VICTORIA_LOGS_URL"));
        }

        let max_connections = parsed("RUNTIME_STREAM_MAX_CONNECTIONS", 1024)?;
        let max_fanout_groups = parsed("RUNTIME_STREAM_MAX_FANOUT_GROUPS", 256)?;
        let max_buffered_events = parsed("RUNTIME_STREAM_MAX_BUFFERED_EVENTS", 128)?;
        let max_lifetime_seconds = parsed("RUNTIME_STREAM_MAX_LIFETIME_SECONDS", 300_u64)?;
        let heartbeat_seconds = parsed("RUNTIME_STREAM_HEARTBEAT_SECONDS", 15_u64)?;
        let query_interval_ms = parsed("RUNTIME_STREAM_QUERY_INTERVAL_MS", 1_000_u64)?;
        let max_snapshot_seconds = parsed("RUNTIME_STREAM_MAX_SNAPSHOT_SECONDS", 300_u64)?;
        let max_event_bytes = parsed("RUNTIME_STREAM_MAX_EVENT_BYTES", 256 * 1024)?;
        let max_log_lines = parsed("RUNTIME_STREAM_MAX_LOG_LINES", 100)?;
        if max_connections == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_CONNECTIONS"));
        }
        if max_fanout_groups == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_FANOUT_GROUPS"));
        }
        if max_buffered_events == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_BUFFERED_EVENTS"));
        }
        if max_lifetime_seconds == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_LIFETIME_SECONDS"));
        }
        if max_lifetime_seconds > MAX_STREAM_LIFETIME_SECONDS {
            return Err(ConfigError::Limit("RUNTIME_STREAM_MAX_LIFETIME_SECONDS"));
        }
        if heartbeat_seconds == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_HEARTBEAT_SECONDS"));
        }
        if query_interval_ms == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_QUERY_INTERVAL_MS"));
        }
        if max_snapshot_seconds == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_SNAPSHOT_SECONDS"));
        }
        if max_snapshot_seconds > MAX_SNAPSHOT_SECONDS {
            return Err(ConfigError::Limit("RUNTIME_STREAM_MAX_SNAPSHOT_SECONDS"));
        }
        if max_event_bytes == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_EVENT_BYTES"));
        }
        if max_event_bytes > MAX_EVENT_BYTES {
            return Err(ConfigError::Limit("RUNTIME_STREAM_MAX_EVENT_BYTES"));
        }
        if max_log_lines == 0 {
            return Err(ConfigError::Zero("RUNTIME_STREAM_MAX_LOG_LINES"));
        }
        if max_log_lines > MAX_LOG_LINES {
            return Err(ConfigError::Limit("RUNTIME_STREAM_MAX_LOG_LINES"));
        }
        Ok(Self {
            listen_addr,
            zone_id,
            victoria_metrics_url,
            victoria_logs_url,
            max_connections,
            max_fanout_groups,
            max_buffered_events,
            max_lifetime: Duration::from_secs(max_lifetime_seconds),
            heartbeat: Duration::from_secs(heartbeat_seconds),
            query_interval: Duration::from_millis(query_interval_ms),
            max_snapshot: Duration::from_secs(max_snapshot_seconds),
            max_event_bytes,
            max_log_lines,
        })
    }
}

fn required(name: &'static str) -> Result<String, ConfigError> {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .ok_or(ConfigError::Missing(name))
}

fn parsed<T>(name: &'static str, default: T) -> Result<T, ConfigError>
where
    T: std::str::FromStr,
{
    match env::var(name) {
        Ok(value) => value.parse().map_err(|_| ConfigError::Invalid(name)),
        Err(_) => Ok(default),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn duration_budget_is_not_zero() {
        let config = Config {
            listen_addr: "127.0.0.1:8080".parse().unwrap(),
            zone_id: Uuid::new_v4(),
            victoria_metrics_url: "http://metrics".into(),
            victoria_logs_url: "http://logs".into(),
            max_connections: 1,
            max_fanout_groups: 1,
            max_buffered_events: 1,
            max_lifetime: Duration::from_secs(5),
            heartbeat: Duration::from_secs(1),
            query_interval: Duration::from_millis(100),
            max_snapshot: Duration::from_secs(1),
            max_event_bytes: 256 * 1024,
            max_log_lines: 100,
        };
        assert!(config.max_lifetime > config.heartbeat);
    }
}
