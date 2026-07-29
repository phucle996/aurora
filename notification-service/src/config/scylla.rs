use super::environment::{ConfigError, Environment};
use std::path::PathBuf;
use std::time::Duration;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ScyllaTlsMode {
    Disabled,
    Server,
    Mutual,
}

#[derive(Clone, Debug)]
pub struct ScyllaTlsConfig {
    pub mode: ScyllaTlsMode,
    pub ca_cert: Option<PathBuf>,
    pub client_cert: Option<PathBuf>,
    pub client_key: Option<PathBuf>,
}

#[derive(Clone, Debug)]
pub struct ScyllaConfig {
    pub contact_points: Vec<String>,
    pub local_dc: String,
    pub keyspace: String,
    pub username: String,
    pub password: String,
    pub connect_timeout: Duration,
    pub request_timeout: Duration,
    pub replication_factor: usize,
    pub auto_schema: bool,
    pub tls: ScyllaTlsConfig,
}

impl ScyllaConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        Ok(Self {
            // Scylla endpoint, keyspace, credentials and TLS identity are
            // resolved from the Central Notification capability in Vault.
            contact_points: Vec::new(),
            local_dc: String::new(),
            keyspace: String::new(),
            username: String::new(),
            password: String::new(),
            connect_timeout: Duration::from_millis(environment.bounded_u64(
                "SCYLLA_CONNECT_TIMEOUT_MS",
                5_000,
                100,
                30_000,
            )?),
            request_timeout: Duration::from_millis(environment.bounded_u64(
                "SCYLLA_REQUEST_TIMEOUT_MS",
                3_000,
                100,
                30_000,
            )?),
            replication_factor: environment.bounded_usize("SCYLLA_REPLICATION_FACTOR", 3, 1, 9)?,
            auto_schema: environment.required_bool("SCYLLA_AUTO_SCHEMA")?,
            tls: ScyllaTlsConfig {
                mode: ScyllaTlsMode::Disabled,
                ca_cert: None,
                client_cert: None,
                client_key: None,
            },
        })
    }
}

fn valid_identifier(value: &str) -> bool {
    value.len() <= 48
        && value.as_bytes().first().is_some_and(u8::is_ascii_lowercase)
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}

#[cfg(test)]
mod tests {
    use super::valid_identifier;

    #[test]
    fn keyspace_identifier_is_strictly_bounded() {
        assert!(valid_identifier("notification_timeline"));
        assert!(!valid_identifier("Notification-Timeline"));
        assert!(!valid_identifier("1timeline"));
    }
}
