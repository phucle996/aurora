mod environment;
mod kafka;
mod nats_core;
mod otel;
mod postgres;
mod shared_redis;
mod tls;
mod vault;
mod workflows;

pub use kafka::{KafkaConfig, KafkaSecurityProtocol};
pub use nats_core::{NatsAuthMode, NatsCoreConfig};
pub use otel::OtelConfig;
pub use postgres::{PostgresConfig, PostgresTlsMode};
pub use shared_redis::SharedRedisConfig;
pub use tls::{TlsClientConfig, TlsTrustSource};
pub use vault::VaultConfig;
pub use workflows::{OwnershipWorkflowConfig, WorkflowConfig};

use environment::Environment;

/// Secret-bearing application configuration. Deliberately no Debug derive:
/// accidental bootstrap diagnostics must not serialize DSNs, passwords or tokens.
#[derive(Clone)]
pub struct Config {
    pub postgres: PostgresConfig,
    pub shared_redis: SharedRedisConfig,
    pub kafka: KafkaConfig,
    pub nats_core: NatsCoreConfig,
    pub otel: OtelConfig,
    pub workflows: WorkflowConfig,
    pub vault: VaultConfig,
}

impl Config {
    pub async fn load() -> Result<Self, String> {
        let environment = Environment::capture();
        Self::from_environment(&environment)
    }

    fn from_environment(environment: &Environment) -> Result<Self, String> {
        let vault = VaultConfig::load(environment)?;
        Ok(Self {
            // Enabled Vault mode allows the connection URL to be absent from
            // the environment; the placeholder is replaced before startup
            // returns to main.
            postgres: PostgresConfig::load(environment)?,
            shared_redis: SharedRedisConfig::load(environment)?,
            kafka: KafkaConfig::load(environment)?,
            nats_core: NatsCoreConfig::load(environment)?,
            otel: OtelConfig::load(environment)?,
            workflows: WorkflowConfig::load(environment)?,
            vault,
        })
    }
}

pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|hostname| hostname.to_string_lossy().into_owned())
            .unwrap_or_else(|_| "unknown".to_owned())
    })
}

#[cfg(test)]
mod tests {
    use super::{Config, Environment};

    const REQUIRED_DEVELOPMENT_ENV: &[(&str, &str)] = &[
        ("VAULT_ADDR", "http://vault:8200"),
        ("POSTGRES_TLS_MODE", "disable"),
        ("SHARED_REDIS_AOF_REPLICA_ACKS", "0"),
        ("KAFKA_TOPIC_PREFIX", "aurora"),
        ("OTEL_ENABLED", "false"),
        ("REPLICATION_SLOT_NAME", "outbox_slot"),
        ("PUBLICATION_NAME", "outbox_pub"),
        ("CDC_SOURCES", "mail.mail_outbox_records"),
    ];

    #[test]
    fn explicit_development_contract_loads_without_security_fallbacks() {
        let environment = Environment::from_pairs(REQUIRED_DEVELOPMENT_ENV);
        assert!(Config::from_environment(&environment).is_ok());
    }

    #[test]
    fn missing_kafka_topic_prefix_fails_closed() {
        let values = REQUIRED_DEVELOPMENT_ENV
            .iter()
            .copied()
            .filter(|(name, _)| *name != "KAFKA_TOPIC_PREFIX")
            .collect::<Vec<_>>();
        let environment = Environment::from_pairs(&values);
        let error = Config::from_environment(&environment)
            .err()
            .expect("missing Kafka topic prefix must fail");
        assert!(error.contains("KAFKA_TOPIC_PREFIX"));
    }
}
