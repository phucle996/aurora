use std::{env, net::SocketAddr, path::PathBuf, time::Duration};

#[derive(Clone)]
pub struct Config {
    pub listen_addr: SocketAddr,
    pub orchestrator_enabled: bool,
    pub metering_enabled: bool,
    pub zone_id: String,
    pub public_base_url: String,
    pub ticket_ttl: Duration,
    pub nats_zone_url: String,
    pub clickhouse_url: String,
    pub kafka_bootstrap_servers: String,
    pub kafka_security_protocol: String,
    pub kafka_username: Option<String>,
    pub kafka_password: Option<String>,
    pub kafka_ca_cert: Option<String>,
    pub kafka_topic_prefix: String,
    pub minio_host: Option<String>,
    pub minio_port: Option<u16>,
    pub minio_access_key: Option<String>,
    pub minio_secret_key: Option<String>,
    pub proxmox_api_url: String,
    pub proxmox_api_token: String,
    pub proxmox_tls_insecure: bool,
    pub stalwart_jmap_url: String,
    pub stalwart_reporter_bearer_token: String,
    pub mail_health_observe_interval_ms: u64,
    pub nats_ca: PathBuf,
    pub nats_cert: PathBuf,
    pub nats_key: PathBuf,
    pub nats_creds: PathBuf,
    pub nats_timeout: Duration,
    pub required_replicas: usize,
    pub control_assignment_shards: usize,
    pub control_max_concurrency: u32,
    pub control_capacity_weight: u32,
    pub min_workers: usize,
    pub max_workers: usize,
    pub storage_scan_interval: Duration,
    pub storage_scan_batch_size: usize,
    pub storage_scan_batch_pause: Duration,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let listen_addr = required("ZONE_CONTROL_LISTEN")?
            .parse()
            .map_err(|_| "ZONE_CONTROL_LISTEN is invalid".to_string())?;
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
        let control_assignment_shards = parsed("ZONE_CONTROL_ASSIGNMENT_SHARDS", 16_usize)?;
        if !(1..=256).contains(&control_assignment_shards) {
            return Err("ZONE_CONTROL_ASSIGNMENT_SHARDS must be 1..256".to_string());
        }
        let control_max_concurrency = parsed("ZONE_CONTROL_MAX_CONCURRENCY", 32_u32)?;
        if !(1..=512).contains(&control_max_concurrency) {
            return Err("ZONE_CONTROL_MAX_CONCURRENCY must be 1..512".to_string());
        }
        let control_capacity_weight = parsed("ZONE_CONTROL_CAPACITY_WEIGHT", 1_u32)?;
        if !(1..=100).contains(&control_capacity_weight) {
            return Err("ZONE_CONTROL_CAPACITY_WEIGHT must be 1..100".to_string());
        }
        let min_workers = parsed("MIN_WORKERS", 1_usize)?.clamp(1, 512);
        let max_workers = parsed("MAX_WORKERS", 32_usize)?.clamp(min_workers, 2_048);
        let storage_scan_interval_seconds = parsed("STORAGE_SCAN_INTERVAL_SECONDS", 3_600_u64)?;
        if storage_scan_interval_seconds != 3_600 {
            return Err(
                "STORAGE_SCAN_INTERVAL_SECONDS must be exactly 3600 for hourly billing".to_string(),
            );
        }
        let storage_scan_batch_size = parsed("STORAGE_SCAN_BATCH_SIZE", 32_usize)?.clamp(1, 1_000);
        let storage_scan_batch_pause_ms =
            parsed("STORAGE_SCAN_BATCH_PAUSE_MS", 250_u64)?.clamp(0, 60_000);
        Ok(Self {
            listen_addr,
            // Gate B is complete: Zone Control must own every Zone-wide control
            // workflow. An explicit switch lets a deployment fail fast instead
            // of silently reintroducing a legacy owner.
            orchestrator_enabled: parsed_bool("ZONE_CONTROL_ORCHESTRATOR_ENABLED", true)?,
            metering_enabled: parsed_bool("ZONE_CONTROL_METERING_ENABLED", true)?,
            zone_id,
            public_base_url,
            ticket_ttl: Duration::from_secs(ticket_ttl_seconds),
            nats_zone_url: required("NATS_ZONE_URL")?,
            clickhouse_url: required("CLICKHOUSE_URL")?,
            kafka_bootstrap_servers: required("KAFKA_BOOTSTRAP_SERVERS")?,
            kafka_security_protocol: required("KAFKA_SECURITY_PROTOCOL")?.to_ascii_lowercase(),
            kafka_username: optional("KAFKA_USERNAME"),
            kafka_password: optional("KAFKA_PASSWORD"),
            kafka_ca_cert: optional("KAFKA_CA_CERT"),
            kafka_topic_prefix: required("KAFKA_TOPIC_PREFIX")?,
            minio_host: optional("MINIO_HOST"),
            minio_port: optional("MINIO_PORT")
                .map(|value| value.parse())
                .transpose()
                .map_err(|_| "MINIO_PORT is invalid".to_string())?,
            minio_access_key: optional("MINIO_ACCESS_KEY"),
            minio_secret_key: optional("MINIO_SECRET_KEY"),
            proxmox_api_url: optional("PROXMOX_API_URL").unwrap_or_default(),
            proxmox_api_token: optional("PROXMOX_API_TOKEN").unwrap_or_default(),
            proxmox_tls_insecure: parsed_bool("PROXMOX_TLS_INSECURE", false)?,
            stalwart_jmap_url: optional("STALWART_JMAP_URL").unwrap_or_default(),
            stalwart_reporter_bearer_token: optional("STALWART_REPORTER_BEARER_TOKEN")
                .unwrap_or_default(),
            mail_health_observe_interval_ms: parsed("MAIL_HEALTH_OBSERVE_INTERVAL_MS", 10_000_u64)?
                .clamp(5_000, 120_000),
            nats_ca: PathBuf::from(required("NATS_ZONE_TLS_CA")?),
            nats_cert: PathBuf::from(required("NATS_ZONE_TLS_CERT")?),
            nats_key: PathBuf::from(required("NATS_ZONE_TLS_KEY")?),
            nats_creds: PathBuf::from(required("NATS_ZONE_CREDS")?),
            nats_timeout: Duration::from_millis(parsed("NATS_ZONE_REQUEST_TIMEOUT_MS", 2_000_u64)?),
            required_replicas,
            control_assignment_shards,
            control_max_concurrency,
            control_capacity_weight,
            min_workers,
            max_workers,
            storage_scan_interval: Duration::from_secs(storage_scan_interval_seconds),
            storage_scan_batch_size,
            storage_scan_batch_pause: Duration::from_millis(storage_scan_batch_pause_ms),
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

fn parsed_bool(name: &str, default: bool) -> Result<bool, String> {
    match env::var(name) {
        Ok(value) => match value.trim().to_ascii_lowercase().as_str() {
            "1" | "true" | "yes" => Ok(true),
            "0" | "false" | "no" => Ok(false),
            _ => Err(format!("{name} is invalid")),
        },
        Err(_) => Ok(default),
    }
}

fn optional(name: &str) -> Option<String> {
    env::var(name).ok().filter(|value| !value.trim().is_empty())
}
