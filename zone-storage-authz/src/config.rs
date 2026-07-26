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
    pub zone_id: String,
    pub nats_zone_url: String,
    pub nats_ca: Option<PathBuf>,
    pub nats_cert: Option<PathBuf>,
    pub nats_key: Option<PathBuf>,
    pub nats_creds: Option<PathBuf>,
    pub server_cert: PathBuf,
    pub server_key: PathBuf,
    pub client_ca: PathBuf,
    pub public_keys: HashMap<String, [u8; 32]>,
    pub access_cache_capacity: u64,
    pub access_cache_ttl: Duration,
    pub replay_cache_capacity: u64,
}

impl Config {
    pub fn from_env() -> Result<Self, AuthzError> {
        let listen_addr = required("ZONE_STORAGE_AUTHZ_LISTEN")?
            .parse()
            .map_err(|_| {
                AuthzError::Configuration("ZONE_STORAGE_AUTHZ_LISTEN is invalid".into())
            })?;
        let zone_id = required("ZONE_ID")?;
        uuid::Uuid::parse_str(&zone_id)
            .map_err(|_| AuthzError::Configuration("ZONE_ID must be a UUID".into()))?;
        let public_keys_json = required("STORAGE_ASSERTION_PUBLIC_KEYS")?;
        let encoded: HashMap<String, String> =
            serde_json::from_str(&public_keys_json).map_err(|_| {
                AuthzError::Configuration(
                    "STORAGE_ASSERTION_PUBLIC_KEYS must be a JSON object".into(),
                )
            })?;
        if encoded.is_empty() {
            return Err(AuthzError::Configuration(
                "at least one assertion public key is required".into(),
            ));
        }
        let mut public_keys = HashMap::with_capacity(encoded.len());
        for (key_id, value) in encoded {
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
        Ok(Self {
            listen_addr,
            zone_id,
            nats_zone_url: required("NATS_ZONE_URL")?,
            nats_ca: optional_path("NATS_ZONE_TLS_CA"),
            nats_cert: optional_path("NATS_ZONE_TLS_CERT"),
            nats_key: optional_path("NATS_ZONE_TLS_KEY"),
            nats_creds: optional_path("NATS_ZONE_CREDS"),
            server_cert: PathBuf::from(required("ZONE_STORAGE_AUTHZ_TLS_CERT")?),
            server_key: PathBuf::from(required("ZONE_STORAGE_AUTHZ_TLS_KEY")?),
            client_ca: PathBuf::from(required("ZONE_STORAGE_AUTHZ_TLS_CLIENT_CA")?),
            public_keys,
            access_cache_capacity: parsed("STORAGE_ACCESS_CACHE_CAPACITY", 100_000)?,
            access_cache_ttl: Duration::from_secs(parsed("STORAGE_ACCESS_CACHE_TTL_SECONDS", 2)?),
            replay_cache_capacity: parsed("STORAGE_ASSERTION_REPLAY_CAPACITY", 1_000_000)?,
        })
    }
}

fn required(name: &str) -> Result<String, AuthzError> {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| AuthzError::Configuration(format!("{name} is required")))
}

fn optional_path(name: &str) -> Option<PathBuf> {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .map(PathBuf::from)
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
