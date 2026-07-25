use super::environment::{ConfigError, Environment};
use std::time::Duration;

#[derive(Clone, Debug)]
pub struct RedisConfig {
    pub url: String,
    pub connect_timeout: Duration,
    pub auth_timeout: Duration,
    pub auth_max_pending: usize,
    pub reconnect_initial: Duration,
    pub reconnect_max: Duration,
}

impl RedisConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        let url = environment.required("SHARED_REDIS_URL")?;
        if !url.starts_with("redis://") && !url.starts_with("rediss://") {
            return Err(ConfigError::Missing("SHARED_REDIS_URL"));
        }

        Ok(Self {
            url,
            connect_timeout: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_REDIS_CONNECT_TIMEOUT_MS",
                5_000,
                100,
                30_000,
            )?),
            auth_timeout: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_AUTH_TIMEOUT_MS",
                5_000,
                100,
                30_000,
            )?),
            auth_max_pending: environment.bounded_usize(
                "NOTIFICATION_AUTH_MAX_PENDING",
                8_192,
                128,
                262_144,
            )?,
            reconnect_initial: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_REDIS_RECONNECT_INITIAL_MS",
                500,
                50,
                30_000,
            )?),
            reconnect_max: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_REDIS_RECONNECT_MAX_MS",
                30_000,
                500,
                300_000,
            )?),
        })
    }
}
