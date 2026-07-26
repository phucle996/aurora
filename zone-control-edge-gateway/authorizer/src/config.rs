use std::collections::HashMap;
use std::env;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::time::Duration;

use base64::Engine;

use crate::error::AuthzError;

#[derive(Clone)]
pub struct Config {
    pub listen_addr: SocketAddr,
    pub telemetry_addr: SocketAddr,
    pub zone_id: String,
    pub nats_zone_url: String,
    pub nats_ca: PathBuf,
    pub nats_cert: PathBuf,
    pub nats_key: PathBuf,
    pub nats_creds: PathBuf,
    pub nats_connect_timeout: Duration,
    pub nats_request_timeout: Duration,
    pub server_cert: PathBuf,
    pub server_key: PathBuf,
    pub client_ca: PathBuf,
    pub public_keys: HashMap<String, [u8; 32]>,
    pub access_cache_capacity: u64,
    pub access_cache_ttl: Duration,
    pub access_read_timeout: Duration,
    pub access_required_replicas: usize,
    pub replay_cache_capacity: u64,
    pub max_inflight_checks: usize,
}

impl Config {
    pub fn from_env() -> Result<Self, AuthzError> {
        let listen_addr = required("ZONE_CONTROL_AUTHORIZER_LISTEN")?
            .parse()
            .map_err(|_| {
                AuthzError::Configuration("ZONE_CONTROL_AUTHORIZER_LISTEN is invalid".into())
            })?;
        let telemetry_addr = env::var("ZONE_CONTROL_TELEMETRY_LISTEN")
            .unwrap_or_else(|_| "0.0.0.0:9090".to_string())
            .parse()
            .map_err(|_| {
                AuthzError::Configuration("ZONE_CONTROL_TELEMETRY_LISTEN is invalid".into())
            })?;
        if listen_addr == telemetry_addr {
            return Err(AuthzError::Configuration(
                "gRPC and telemetry listeners must use different addresses".into(),
            ));
        }
        let zone_id = required("ZONE_ID")?;
        let parsed_zone_id = uuid::Uuid::parse_str(&zone_id)
            .map_err(|_| AuthzError::Configuration("ZONE_ID must be a UUID".into()))?;
        if parsed_zone_id.is_nil() {
            return Err(AuthzError::Configuration(
                "ZONE_ID must be a non-nil UUID".into(),
            ));
        }
        let public_keys_json = required("ZONE_CONTROL_ASSERTION_PUBLIC_KEYS")?;
        let encoded: HashMap<String, String> =
            serde_json::from_str(&public_keys_json).map_err(|_| {
                AuthzError::Configuration(
                    "ZONE_CONTROL_ASSERTION_PUBLIC_KEYS must be a JSON object".into(),
                )
            })?;
        if encoded.is_empty() {
            return Err(AuthzError::Configuration(
                "at least one assertion public key is required".into(),
            ));
        }
        let mut public_keys = HashMap::with_capacity(encoded.len());
        for (key_id, value) in encoded {
            if key_id.is_empty()
                || key_id.len() > 128
                || !key_id.bytes().all(|byte| {
                    byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-' | b':')
                })
            {
                return Err(AuthzError::Configuration(
                    "assertion public key id is invalid".into(),
                ));
            }
            let bytes = base64::engine::general_purpose::STANDARD
                .decode(value)
                .map_err(|_| {
                    AuthzError::Configuration(format!("public key {key_id} is not base64"))
                })?;
            let key: [u8; 32] = bytes.try_into().map_err(|_| {
                AuthzError::Configuration(format!("public key {key_id} must be 32 bytes"))
            })?;
            public_keys.insert(key_id, key);
        }
        let access_required_replicas = parsed("ZONE_ACCESS_REQUIRED_REPLICAS", 3_usize)?;
        if access_required_replicas == 0 {
            return Err(AuthzError::Configuration(
                "ZONE_ACCESS_REQUIRED_REPLICAS must be greater than zero".into(),
            ));
        }
        let max_inflight_checks = parsed("ZONE_CONTROL_MAX_INFLIGHT_CHECKS", 512_usize)?;
        if max_inflight_checks == 0 {
            return Err(AuthzError::Configuration(
                "ZONE_CONTROL_MAX_INFLIGHT_CHECKS must be greater than zero".into(),
            ));
        }
        let nats_connect_timeout_ms = parsed("NATS_ZONE_CONNECT_TIMEOUT_MS", 5_000_u64)?;
        let nats_request_timeout_ms = parsed("NATS_ZONE_REQUEST_TIMEOUT_MS", 2_000_u64)?;
        let access_cache_capacity = parsed("ZONE_ACCESS_CACHE_CAPACITY", 100_000_u64)?;
        let access_cache_ttl_seconds = parsed("ZONE_ACCESS_CACHE_TTL_SECONDS", 2_u64)?;
        let access_read_timeout_ms = parsed("ZONE_ACCESS_READ_TIMEOUT_MS", 250_u64)?;
        let replay_cache_capacity =
            parsed("ZONE_CONTROL_ASSERTION_REPLAY_CAPACITY", 1_000_000_u64)?;
        if nats_connect_timeout_ms == 0
            || nats_request_timeout_ms == 0
            || access_cache_capacity == 0
            || access_cache_ttl_seconds == 0
            || access_read_timeout_ms == 0
            || replay_cache_capacity == 0
        {
            return Err(AuthzError::Configuration(
                "timeouts and cache capacities must be greater than zero".into(),
            ));
        }
        Ok(Self {
            listen_addr,
            telemetry_addr,
            zone_id,
            nats_zone_url: required("NATS_ZONE_URL")?,
            nats_ca: PathBuf::from(required("NATS_ZONE_TLS_CA")?),
            nats_cert: PathBuf::from(required("NATS_ZONE_TLS_CERT")?),
            nats_key: PathBuf::from(required("NATS_ZONE_TLS_KEY")?),
            nats_creds: PathBuf::from(required("NATS_ZONE_CREDS")?),
            nats_connect_timeout: Duration::from_millis(nats_connect_timeout_ms),
            nats_request_timeout: Duration::from_millis(nats_request_timeout_ms),
            server_cert: PathBuf::from(required("ZONE_CONTROL_AUTHORIZER_TLS_CERT")?),
            server_key: PathBuf::from(required("ZONE_CONTROL_AUTHORIZER_TLS_KEY")?),
            client_ca: PathBuf::from(required("ZONE_CONTROL_AUTHORIZER_TLS_CLIENT_CA")?),
            public_keys,
            access_cache_capacity,
            access_cache_ttl: Duration::from_secs(access_cache_ttl_seconds),
            access_read_timeout: Duration::from_millis(access_read_timeout_ms),
            access_required_replicas,
            replay_cache_capacity,
            max_inflight_checks,
        })
    }
}

fn required(name: &str) -> Result<String, AuthzError> {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| AuthzError::Configuration(format!("{name} is required")))
}

fn parsed<T>(name: &str, default: T) -> Result<T, AuthzError>
where
    T: std::str::FromStr,
{
    match env::var(name) {
        Ok(value) => value
            .parse()
            .map_err(|_| AuthzError::Configuration(format!("{name} is invalid"))),
        Err(_) => Ok(default),
    }
}
