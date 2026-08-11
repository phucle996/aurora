use std::{env, net::SocketAddr, path::PathBuf, time::Duration};

#[derive(Clone)]
pub struct Config {
    pub listen_addr: SocketAddr,
    pub zone_id: String,
    pub public_base_url: String,
    pub ticket_ttl: Duration,
    pub nats_zone_url: String,
    pub nats_ca: PathBuf,
    pub nats_cert: PathBuf,
    pub nats_key: PathBuf,
    pub nats_creds: PathBuf,
    pub nats_timeout: Duration,
    pub required_replicas: usize,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let listen_addr = required("ZONE_TRANSFER_TICKET_ISSUER_LISTEN")?
            .parse()
            .map_err(|_| "ZONE_TRANSFER_TICKET_ISSUER_LISTEN is invalid".to_string())?;
        let zone_id = required("ZONE_ID")?;
        if uuid::Uuid::parse_str(&zone_id).is_err()
            || uuid::Uuid::parse_str(&zone_id).is_ok_and(|id| id.is_nil())
        {
            return Err("ZONE_ID must be a non-nil UUID".to_string());
        }
        let public_base_url = required("ZONE_PUBLIC_BASE_URL")?
            .trim_end_matches('/')
            .to_string();
        if !public_base_url.starts_with("https://") {
            return Err("ZONE_PUBLIC_BASE_URL must use https".to_string());
        }
        let ticket_ttl_seconds = parsed("ZONE_TRANSFER_TICKET_TTL_SECONDS", 60_u64)?;
        if !(15..=300).contains(&ticket_ttl_seconds) {
            return Err("ZONE_TRANSFER_TICKET_TTL_SECONDS must be 15..300".to_string());
        }
        let required_replicas = parsed("ZONE_TRANSFER_KV_REQUIRED_REPLICAS", 3_usize)?;
        if !matches!(required_replicas, 1 | 3 | 5) {
            return Err("ZONE_TRANSFER_KV_REQUIRED_REPLICAS must be 1, 3 or 5".to_string());
        }
        Ok(Self {
            listen_addr,
            zone_id,
            public_base_url,
            ticket_ttl: Duration::from_secs(ticket_ttl_seconds),
            nats_zone_url: required("NATS_ZONE_URL")?,
            nats_ca: PathBuf::from(required("NATS_ZONE_TLS_CA")?),
            nats_cert: PathBuf::from(required("NATS_ZONE_TLS_CERT")?),
            nats_key: PathBuf::from(required("NATS_ZONE_TLS_KEY")?),
            nats_creds: PathBuf::from(required("NATS_ZONE_CREDS")?),
            nats_timeout: Duration::from_millis(parsed("NATS_ZONE_REQUEST_TIMEOUT_MS", 2_000_u64)?),
            required_replicas,
        })
    }
}

fn required(name: &str) -> Result<String, String> {
    env::var(name).map_err(|_| format!("{name} is required"))
}

fn parsed<T: std::str::FromStr>(name: &str, default: T) -> Result<T, String> {
    match env::var(name) {
        Ok(value) => value.parse().map_err(|_| format!("{name} is invalid")),
        Err(_) => Ok(default),
    }
}
