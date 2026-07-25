use super::environment::{ConfigError, Environment};

#[derive(Clone, Debug)]
pub struct OtelConfig {
    pub exporter_endpoint: String,
}

impl OtelConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        let exporter_endpoint = environment.required("OTEL_EXPORTER_OTLP_ENDPOINT")?;
        if !exporter_endpoint.starts_with("http://")
            && !exporter_endpoint.starts_with("https://")
            && !exporter_endpoint.starts_with("grpc://")
        {
            return Err(ConfigError::Missing("OTEL_EXPORTER_OTLP_ENDPOINT"));
        }
        Ok(Self { exporter_endpoint })
    }
}
