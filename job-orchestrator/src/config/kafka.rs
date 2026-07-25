use super::environment::{normalized, validate_identifier, Environment};
use super::tls::TlsClientConfig;
use std::str::FromStr;

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum KafkaSecurityProtocol {
    Plaintext,
    Ssl,
    SaslPlaintext,
    SaslPlainSsl,
}

impl KafkaSecurityProtocol {
    pub fn uses_tls(self) -> bool {
        matches!(self, Self::Ssl | Self::SaslPlainSsl)
    }

    pub fn uses_sasl(self) -> bool {
        matches!(self, Self::SaslPlaintext | Self::SaslPlainSsl)
    }
}

impl FromStr for KafkaSecurityProtocol {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match normalized(value).as_str() {
            "plaintext" => Ok(Self::Plaintext),
            "ssl" => Ok(Self::Ssl),
            "sasl_plaintext" => Ok(Self::SaslPlaintext),
            "sasl_plain_ssl" => Ok(Self::SaslPlainSsl),
            _ => Err(
                "KAFKA_SECURITY_PROTOCOL must be explicitly set to plaintext, ssl, sasl_plaintext or sasl_plain_ssl"
                    .to_owned(),
            ),
        }
    }
}

impl std::fmt::Display for KafkaSecurityProtocol {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Plaintext => "plaintext",
            Self::Ssl => "ssl",
            Self::SaslPlaintext => "sasl_plaintext",
            Self::SaslPlainSsl => "sasl_plain_ssl",
        })
    }
}

/// Kafka Central transport configuration. Idempotence and acks=all remain
/// compile-time invariants in the producer factory, not environment switches.
#[derive(Clone)]
pub struct KafkaConfig {
    pub bootstrap_servers: String,
    pub security_protocol: KafkaSecurityProtocol,
    pub username: Option<String>,
    pub password: Option<String>,
    pub tls: Option<TlsClientConfig>,
    pub tls_server_name: Option<String>,
    pub topic_prefix: String,
    pub client_id: String,
    pub producer_batch_bytes: usize,
    pub producer_linger_ms: u64,
    pub request_timeout_ms: u64,
    pub delivery_timeout_ms: u64,
    pub metadata_max_age_ms: u64,
    pub producer_retries: u32,
    pub max_in_flight: usize,
    pub consumer_max_poll_records: i32,
    pub consumer_session_timeout_ms: u64,
    pub consumer_heartbeat_interval_ms: u64,
    pub publish_attempts: u32,
    pub publish_retry_delay_ms: u64,
}

impl KafkaConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        let security_protocol: KafkaSecurityProtocol =
            environment.required_enum("KAFKA_SECURITY_PROTOCOL")?;
        let username = environment.optional("KAFKA_USERNAME");
        let password = environment.optional("KAFKA_PASSWORD");
        if security_protocol.uses_sasl() {
            if username.is_none() || password.is_none() {
                return Err(
                    "Kafka SASL protocol requires KAFKA_USERNAME and KAFKA_PASSWORD".to_owned(),
                );
            }
        } else {
            environment.reject_present(
                &["KAFKA_USERNAME", "KAFKA_PASSWORD"],
                "KAFKA_SECURITY_PROTOCOL does not use SASL",
            )?;
        }

        let tls_server_name = environment.optional("KAFKA_TLS_SERVER_NAME");
        let tls = if security_protocol.uses_tls() {
            Some(TlsClientConfig::load(
                environment,
                "KAFKA_TLS",
                "KAFKA_TLS_CA_CERT",
                "KAFKA_TLS_CLIENT_CERT",
                "KAFKA_TLS_CLIENT_KEY",
            )?)
        } else {
            TlsClientConfig::ensure_absent(
                environment,
                "KAFKA_TLS",
                "KAFKA_TLS_CA_CERT",
                "KAFKA_TLS_CLIENT_CERT",
                "KAFKA_TLS_CLIENT_KEY",
                "KAFKA_SECURITY_PROTOCOL does not use TLS",
            )?;
            if tls_server_name.is_some() {
                return Err("KAFKA_TLS_SERVER_NAME requires ssl or sasl_plain_ssl".to_owned());
            }
            None
        };

        let topic_prefix = environment.required("KAFKA_TOPIC_PREFIX")?;
        validate_topic_prefix(&topic_prefix)?;
        let client_id = environment
            .optional("KAFKA_CLIENT_ID")
            .unwrap_or_else(|| "aurora-job-orchestrator".to_owned());
        validate_identifier("KAFKA_CLIENT_ID", &client_id)?;

        let request_timeout_ms =
            environment.bounded("KAFKA_REQUEST_TIMEOUT_MS", 10_000_u64, 1_000, 120_000)?;
        let delivery_timeout_ms =
            environment.bounded("KAFKA_DELIVERY_TIMEOUT_MS", 60_000_u64, 1_000, 600_000)?;
        if delivery_timeout_ms < request_timeout_ms {
            return Err(
                "KAFKA_DELIVERY_TIMEOUT_MS must be greater than or equal to KAFKA_REQUEST_TIMEOUT_MS"
                    .to_owned(),
            );
        }

        let consumer_session_timeout_ms = environment.bounded(
            "KAFKA_CONSUMER_SESSION_TIMEOUT_MS",
            10_000_u64,
            3_000,
            300_000,
        )?;
        let consumer_heartbeat_interval_ms = environment.bounded(
            "KAFKA_CONSUMER_HEARTBEAT_INTERVAL_MS",
            3_000_u64,
            500,
            100_000,
        )?;
        // Three heartbeat opportunities keep normal scheduler jitter from
        // triggering partition churn across healthy JO replicas.
        if consumer_heartbeat_interval_ms.saturating_mul(3) > consumer_session_timeout_ms {
            return Err(
                "KAFKA_CONSUMER_HEARTBEAT_INTERVAL_MS must be at most one third of KAFKA_CONSUMER_SESSION_TIMEOUT_MS"
                    .to_owned(),
            );
        }

        Ok(Self {
            bootstrap_servers: environment.required("KAFKA_BOOTSTRAP_SERVERS")?,
            security_protocol,
            username,
            password,
            tls,
            tls_server_name,
            topic_prefix,
            client_id,
            producer_batch_bytes: environment.bounded(
                "KAFKA_PRODUCER_BATCH_BYTES",
                65_536_usize,
                1_024,
                16_777_216,
            )?,
            producer_linger_ms: environment.bounded("KAFKA_PRODUCER_LINGER_MS", 5_u64, 0, 5_000)?,
            request_timeout_ms,
            delivery_timeout_ms,
            metadata_max_age_ms: environment.bounded(
                "KAFKA_METADATA_MAX_AGE_MS",
                5_000_u64,
                1_000,
                300_000,
            )?,
            producer_retries: environment.bounded("KAFKA_PRODUCER_RETRIES", 10_u32, 1, 100)?,
            max_in_flight: environment.bounded("KAFKA_MAX_IN_FLIGHT", 5_usize, 1, 5)?,
            consumer_max_poll_records: environment.bounded(
                "KAFKA_CONSUMER_MAX_POLL_RECORDS",
                32_i32,
                1,
                10_000,
            )?,
            consumer_session_timeout_ms,
            consumer_heartbeat_interval_ms,
            publish_attempts: environment.bounded("KAFKA_PUBLISH_ATTEMPTS", 5_u32, 1, 20)?,
            publish_retry_delay_ms: environment.bounded(
                "KAFKA_PUBLISH_RETRY_DELAY_MS",
                300_u64,
                10,
                10_000,
            )?,
        })
    }
}

fn validate_topic_prefix(value: &str) -> Result<(), String> {
    if value.is_empty()
        || value.len() > 128
        || value.starts_with('.')
        || value.ends_with('.')
        || value
            .bytes()
            .any(|byte| !byte.is_ascii_alphanumeric() && !matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(
            "KAFKA_TOPIC_PREFIX must be 1..128 ASCII alphanumeric/dot/underscore/dash characters without leading or trailing dot"
                .to_owned(),
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::KafkaSecurityProtocol;
    use std::str::FromStr;

    #[test]
    fn protocol_values_are_canonical_and_explicit() {
        assert!(KafkaSecurityProtocol::from_str("sasl_plain_ssl").is_ok());
        assert!(KafkaSecurityProtocol::from_str("SASL_SSL").is_err());
        assert!(KafkaSecurityProtocol::from_str("unknown").is_err());
    }
}
