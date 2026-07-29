use super::environment::{normalized, validate_identifier, Environment};
use super::tls::{TlsClientConfig, TlsTrustSource};
use std::str::FromStr;

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum PostgresTlsMode {
    Disable,
    VerifyFull,
}

impl FromStr for PostgresTlsMode {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match normalized(value).as_str() {
            "disable" => Ok(Self::Disable),
            "verify_full" => Ok(Self::VerifyFull),
            _ => {
                Err("POSTGRES_TLS_MODE must be explicitly set to disable or verify_full".to_owned())
            }
        }
    }
}

impl std::fmt::Display for PostgresTlsMode {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Disable => "disable",
            Self::VerifyFull => "verify_full",
        })
    }
}

/// PostgreSQL SQL and logical-replication connection contract.
/// The same TLS identity is used by both protocol paths.
#[derive(Clone)]
pub struct PostgresConfig {
    pub database_url: String,
    pub application_name: String,
    pub connect_timeout_secs: u64,
    pub tcp_user_timeout_ms: u64,
    pub keepalive_idle_secs: u64,
    pub keepalive_interval_secs: u64,
    pub keepalive_retries: u32,
    pub statement_timeout_ms: u64,
    pub lock_timeout_ms: u64,
    pub idle_transaction_timeout_ms: u64,
    pub tls_mode: PostgresTlsMode,
    pub tls: Option<TlsClientConfig>,
    pub replication_sni: Option<String>,
}

impl PostgresConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        // The URL is resolved by infra::postgres from Vault before any
        // PostgreSQL or logical-replication connection is opened.
        let database_url = String::new();

        let tls_mode: PostgresTlsMode = environment.required_enum("POSTGRES_TLS_MODE")?;
        let replication_sni = environment.optional("POSTGRES_REPLICATION_SNI");
        let tls = match tls_mode {
            PostgresTlsMode::Disable => {
                TlsClientConfig::ensure_absent(
                    environment,
                    "POSTGRES_TLS",
                    "POSTGRES_TLS_CA_CERT",
                    "POSTGRES_TLS_CLIENT_CERT",
                    "POSTGRES_TLS_CLIENT_KEY",
                    "POSTGRES_TLS_MODE=disable",
                )?;
                if replication_sni.is_some() {
                    return Err(
                        "POSTGRES_REPLICATION_SNI requires POSTGRES_TLS_MODE=verify_full"
                            .to_owned(),
                    );
                }
                None
            }
            PostgresTlsMode::VerifyFull => Some(TlsClientConfig::load(
                environment,
                "POSTGRES_TLS",
                "POSTGRES_TLS_CA_CERT",
                "POSTGRES_TLS_CLIENT_CERT",
                "POSTGRES_TLS_CLIENT_KEY",
            )?),
        };

        let application_name = environment
            .optional("POSTGRES_APPLICATION_NAME")
            .unwrap_or_else(|| "aurora-job-orchestrator".to_owned());
        validate_identifier("POSTGRES_APPLICATION_NAME", &application_name)?;

        Ok(Self {
            database_url,
            application_name,
            connect_timeout_secs: environment.bounded(
                "POSTGRES_CONNECT_TIMEOUT_SECS",
                5_u64,
                1,
                60,
            )?,
            tcp_user_timeout_ms: environment.bounded(
                "POSTGRES_TCP_USER_TIMEOUT_MS",
                15_000_u64,
                1_000,
                300_000,
            )?,
            keepalive_idle_secs: environment.bounded(
                "POSTGRES_KEEPALIVE_IDLE_SECS",
                30_u64,
                1,
                600,
            )?,
            keepalive_interval_secs: environment.bounded(
                "POSTGRES_KEEPALIVE_INTERVAL_SECS",
                10_u64,
                1,
                300,
            )?,
            keepalive_retries: environment.bounded("POSTGRES_KEEPALIVE_RETRIES", 3_u32, 1, 20)?,
            statement_timeout_ms: environment.bounded(
                "POSTGRES_STATEMENT_TIMEOUT_MS",
                5_000_u64,
                100,
                300_000,
            )?,
            lock_timeout_ms: environment.bounded(
                "POSTGRES_LOCK_TIMEOUT_MS",
                1_000_u64,
                50,
                60_000,
            )?,
            idle_transaction_timeout_ms: environment.bounded(
                "POSTGRES_IDLE_TRANSACTION_TIMEOUT_MS",
                5_000_u64,
                100,
                300_000,
            )?,
            tls_mode,
            tls,
            replication_sni,
        })
    }

    pub fn replication_tls(&self) -> pgwire_replication::TlsConfig {
        let Some(tls) = &self.tls else {
            return pgwire_replication::TlsConfig {
                mode: pgwire_replication::SslMode::Disable,
                ..Default::default()
            };
        };
        pgwire_replication::TlsConfig {
            mode: pgwire_replication::SslMode::VerifyFull,
            ca_pem_path: match tls.trust_source {
                TlsTrustSource::System => None,
                TlsTrustSource::File => tls.ca_cert.clone(),
            },
            sni_hostname: self.replication_sni.clone(),
            client_cert_pem_path: tls.client_cert.clone(),
            client_key_pem_path: tls.client_key.clone(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::PostgresTlsMode;
    use std::str::FromStr;

    #[test]
    fn insecure_or_implicit_tls_modes_are_rejected() {
        assert!(PostgresTlsMode::from_str("verify_full").is_ok());
        assert!(PostgresTlsMode::from_str("require").is_err());
        assert!(PostgresTlsMode::from_str("prefer").is_err());
        assert!(PostgresTlsMode::from_str("").is_err());
    }
}
