mod centrifugo;
mod environment;
mod otel;
mod redis;
mod runtime;
mod scylla;
mod timeline;

pub use centrifugo::CentrifugoConfig;
pub use environment::{ConfigError, Environment};
pub use otel::OtelConfig;
pub use redis::RedisConfig;
pub use runtime::RuntimeConfig;
pub use scylla::{ScyllaConfig, ScyllaTlsMode};
pub use timeline::TimelineConfig;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub redis: RedisConfig,
    pub centrifugo: CentrifugoConfig,
    pub otel: OtelConfig,
    pub runtime: RuntimeConfig,
    pub scylla: ScyllaConfig,
    pub timeline: TimelineConfig,
}

impl Config {
    pub fn from_env() -> Result<Self, ConfigError> {
        let environment = Environment::capture();
        let app_port = environment
            .optional("APP_PORT")
            .map(|value| {
                value
                    .parse::<u16>()
                    .map_err(|_| ConfigError::InvalidNumber("APP_PORT"))
            })
            .transpose()?
            .unwrap_or(8_083);
        if app_port == 0 {
            return Err(ConfigError::OutOfRange {
                key: "APP_PORT",
                min: 1,
                max: u16::MAX as u64,
            });
        }

        Ok(Self {
            app_port,
            redis: RedisConfig::from_env(&environment)?,
            centrifugo: CentrifugoConfig::from_env(&environment)?,
            otel: OtelConfig::from_env(&environment)?,
            runtime: RuntimeConfig::from_env(&environment)?,
            scylla: ScyllaConfig::from_env(&environment)?,
            timeline: TimelineConfig::from_env(&environment)?,
        })
    }
}
