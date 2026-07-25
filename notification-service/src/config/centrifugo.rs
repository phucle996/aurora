use super::environment::{ConfigError, Environment};
use std::time::Duration;

#[derive(Clone, Debug)]
pub struct CentrifugoConfig {
    pub api_url: String,
    pub api_key: String,
    pub request_timeout: Duration,
}

impl CentrifugoConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        let api_url = environment.required("CENTRIFUGO_API_URL")?;
        let api_key = environment.required("CENTRIFUGO_API_KEY")?;
        if !api_url.starts_with("http://") && !api_url.starts_with("https://") {
            return Err(ConfigError::Missing("CENTRIFUGO_API_URL"));
        }

        Ok(Self {
            api_url,
            api_key,
            request_timeout: Duration::from_millis(environment.bounded_u64(
                "CENTRIFUGO_REQUEST_TIMEOUT_MS",
                5_000,
                100,
                30_000,
            )?),
        })
    }
}
