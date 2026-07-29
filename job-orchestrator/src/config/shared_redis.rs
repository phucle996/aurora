use super::environment::Environment;
use super::tls::TlsClientConfig;

/// Shared L2 Redis is Central-only bounded state/transport, never Zone storage.
#[derive(Clone)]
pub struct SharedRedisConfig {
    pub url: String,
    pub tls: Option<TlsClientConfig>,
    pub connect_timeout_ms: u64,
    pub response_timeout_ms: u64,
    pub reconnect_retries: usize,
    pub reconnect_base: u64,
    pub reconnect_factor_ms: u64,
    pub aof_replica_acks: i64,
    pub aof_timeout_ms: u64,
}

impl SharedRedisConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        // Endpoint and credential are resolved by infra::redis from Vault.
        let url = String::new();
        let uses_tls = environment.optional_bool("SHARED_REDIS_TLS", false)?;
        let tls = if uses_tls {
            Some(TlsClientConfig::load(
                environment,
                "SHARED_REDIS_TLS",
                "SHARED_REDIS_TLS_CA_CERT",
                "SHARED_REDIS_TLS_CLIENT_CERT",
                "SHARED_REDIS_TLS_CLIENT_KEY",
            )?)
        } else {
            TlsClientConfig::ensure_absent(
                environment,
                "SHARED_REDIS_TLS",
                "SHARED_REDIS_TLS_CA_CERT",
                "SHARED_REDIS_TLS_CLIENT_CERT",
                "SHARED_REDIS_TLS_CLIENT_KEY",
                "Vault Redis URL uses redis://",
            )?;
            None
        };

        Ok(Self {
            url,
            tls,
            connect_timeout_ms: environment.bounded(
                "SHARED_REDIS_CONNECT_TIMEOUT_MS",
                3_000_u64,
                100,
                60_000,
            )?,
            response_timeout_ms: environment.bounded(
                "SHARED_REDIS_RESPONSE_TIMEOUT_MS",
                5_000_u64,
                100,
                120_000,
            )?,
            reconnect_retries: environment.bounded(
                "SHARED_REDIS_RECONNECT_RETRIES",
                6_usize,
                0,
                100,
            )?,
            reconnect_base: environment.bounded("SHARED_REDIS_RECONNECT_BASE", 2_u64, 1, 10)?,
            reconnect_factor_ms: environment.bounded(
                "SHARED_REDIS_RECONNECT_FACTOR_MS",
                100_u64,
                1,
                10_000,
            )?,
            // This is a durability boundary, not a tuning default. Development
            // must explicitly choose zero; production must explicitly choose HA.
            aof_replica_acks: environment.required_bounded(
                "SHARED_REDIS_AOF_REPLICA_ACKS",
                0_i64,
                5,
            )?,
            aof_timeout_ms: environment.bounded(
                "SHARED_REDIS_AOF_TIMEOUT_MS",
                2_000_u64,
                100,
                10_000,
            )?,
        })
    }
}
