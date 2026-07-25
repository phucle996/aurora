use super::environment::{ConfigError, Environment};
use std::time::Duration;

#[derive(Clone, Debug)]
pub struct RuntimeConfig {
    pub stream_batch_size: usize,
    pub stream_claim_idle: Duration,
    pub reconnect_initial: Duration,
    pub reconnect_max: Duration,
    pub shutdown_timeout: Duration,
}

impl RuntimeConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        let reconnect_initial = Duration::from_millis(environment.bounded_u64(
            "NOTIFICATION_RECONNECT_INITIAL_MS",
            500,
            50,
            30_000,
        )?);
        let reconnect_max = Duration::from_millis(environment.bounded_u64(
            "NOTIFICATION_RECONNECT_MAX_MS",
            30_000,
            500,
            300_000,
        )?);
        if reconnect_max < reconnect_initial {
            return Err(ConfigError::OutOfRange {
                key: "NOTIFICATION_RECONNECT_MAX_MS",
                min: reconnect_initial.as_millis() as u64,
                max: 300_000,
            });
        }

        Ok(Self {
            stream_batch_size: environment.bounded_usize(
                "NOTIFICATION_STREAM_BATCH_SIZE",
                16,
                1,
                256,
            )?,
            stream_claim_idle: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_STREAM_CLAIM_IDLE_MS",
                30_000,
                1_000,
                3_600_000,
            )?),
            reconnect_initial,
            reconnect_max,
            shutdown_timeout: Duration::from_millis(environment.bounded_u64(
                "NOTIFICATION_SHUTDOWN_TIMEOUT_MS",
                10_000,
                500,
                120_000,
            )?),
        })
    }
}
