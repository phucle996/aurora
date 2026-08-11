use std::env;
use std::path::PathBuf;
use std::time::Duration;

fn required_env(name: &str) -> Result<String, String> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("{name} must be set and non-empty"))
}

#[derive(Debug, Clone)]
pub struct VaultConfig {
    pub addr: String,
    pub token: Option<String>,
    pub role_id: Option<String>,
    pub secret_id: Option<String>,
    pub kubernetes_role: Option<String>,
    pub kubernetes_jwt_path: PathBuf,
    pub timeout: Duration,
    pub max_retries: usize,
}

/// Cấu hình hệ thống Cost Manager Engine đọc từ các biến môi trường
#[derive(Debug, Clone)]
pub struct Config {
    /// URL kết nối tới ClickHouse (lưu trữ logs metering)
    pub clickhouse_url: String,
    /// URL kết nối tới Redis (cache chặn keys và quản lý locks/checkpoint)
    pub redis_url: String,
    /// Số lượng connection tối đa trong PostgreSQL pool
    pub pg_max_connections: u32,
    /// Số lượng connection tối thiểu trong PostgreSQL pool
    pub pg_min_connections: u32,
    /// Thời gian chờ tối đa để lấy connection từ pool
    pub pg_acquire_timeout: Duration,
    /// Chu kỳ quét dữ liệu tính cước định kỳ
    pub scan_interval: Duration,
    /// Thời gian sống (TTL) của Distributed Lock trên Redis nhằm tránh tranh chấp giữa các Replica
    pub lock_ttl_secs: u64,
    /// Thời gian sống (TTL) của block key trên Redis khi ví của tài khoản bị khóa
    pub block_key_ttl_secs: u64,
    /// Opt-in report-driven storage settlement.  False keeps the legacy
    /// ClickHouse path active during the controlled shadow/cutover phases.
    pub storage_report_settlement_enabled: bool,

    // --- CẤU HÌNH BẢO MẬT KẾT NỐI (TLS/mTLS) ---
    /// PostgreSQL SSL Mode (`disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full`)
    pub pg_ssl_mode: String,
    /// Đường dẫn tới file CA Certificate để xác thực PostgreSQL Server Cert
    pub pg_ssl_root_cert: Option<String>,
    /// Đường dẫn tới file Client Certificate dùng cho mTLS PostgreSQL
    pub pg_ssl_client_cert: Option<String>,
    /// Đường dẫn tới file Client Private Key dùng cho mTLS PostgreSQL
    pub pg_ssl_client_key: Option<String>,

    pub vault: VaultConfig,
}

impl VaultConfig {
    fn from_env() -> Result<Self, String> {
        Ok(Self {
            addr: required_env("VAULT_ADDR")?,
            token: env::var("VAULT_TOKEN")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            role_id: env::var("VAULT_ROLE_ID")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            secret_id: env::var("VAULT_SECRET_ID")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            kubernetes_role: env::var("VAULT_KUBERNETES_ROLE")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            kubernetes_jwt_path: PathBuf::from(
                env::var("VAULT_KUBERNETES_JWT_PATH").unwrap_or_else(|_| {
                    "/var/run/secrets/kubernetes.io/serviceaccount/token".to_owned()
                }),
            ),
            timeout: Duration::from_secs(
                env::var("VAULT_TIMEOUT_SECS")
                    .ok()
                    .and_then(|value| value.parse().ok())
                    .unwrap_or(5),
            ),
            max_retries: env::var("VAULT_MAX_RETRIES")
                .ok()
                .and_then(|value| value.parse().ok())
                .unwrap_or(5)
                .clamp(1, 20),
        })
    }
}

impl Config {
    /// Identity-bearing endpoints and security modes are required. Only
    /// bounded performance/retention controls keep local defaults.
    pub fn from_env() -> Result<Self, String> {
        let clickhouse_url = required_env("CLICKHOUSE_URL")?;

        // Đọc Redis URL cho control plane
        let redis_url = String::new();

        // Cấu hình số kết nối tối đa tới Postgres, mặc định là 10
        let pg_max_connections = env::var("PG_MAX_CONNECTIONS")
            .ok()
            .and_then(|s| s.parse().ok())
            .unwrap_or(10);

        // Cấu hình số kết nối tối thiểu tới Postgres, mặc định là 1
        let pg_min_connections = env::var("PG_MIN_CONNECTIONS")
            .ok()
            .and_then(|s| s.parse().ok())
            .unwrap_or(1);

        // Thời gian chờ kết nối Postgres, mặc định 5 giây
        let pg_acquire_timeout = env::var("PG_ACQUIRE_TIMEOUT_SECS")
            .ok()
            .and_then(|s| s.parse().ok())
            .map(Duration::from_secs)
            .unwrap_or(Duration::from_secs(5));

        // Chu kỳ quét cước, mặc định 30 giây một lần
        let scan_interval = env::var("SCAN_INTERVAL_SECS")
            .ok()
            .and_then(|s| s.parse().ok())
            .map(Duration::from_secs)
            .unwrap_or(Duration::from_secs(30));

        // TTL cho lock chạy đơn bản ghi (HA Distributed Lock), mặc định 25 giây
        let lock_ttl_secs = env::var("LOCK_TTL_SECS")
            .ok()
            .and_then(|s| s.parse().ok())
            .unwrap_or(25);

        // TTL block key Redis, mặc định rất dài (30 ngày) để đảm bảo khóa tài khoản cho đến khi có can thiệp/nạp tiền
        let block_key_ttl_secs = env::var("BLOCK_KEY_TTL_SECS")
            .ok()
            .and_then(|s| s.parse().ok())
            .unwrap_or(2592000); // 30 ngày = 30 * 24 * 3600

        let storage_report_settlement_enabled = match env::var("STORAGE_REPORT_SETTLEMENT_ENABLED")
        {
            Ok(value) => match value.trim().to_ascii_lowercase().as_str() {
                "1" | "true" | "yes" => true,
                "0" | "false" | "no" => false,
                _ => return Err("STORAGE_REPORT_SETTLEMENT_ENABLED is invalid".to_owned()),
            },
            Err(_) => false,
        };

        // --- Đọc cấu hình TLS/mTLS từ biến môi trường ---

        let pg_ssl_mode = required_env("PG_SSL_MODE")?.to_ascii_lowercase();
        if !matches!(
            pg_ssl_mode.as_str(),
            "disable" | "allow" | "prefer" | "require" | "verify-ca" | "verify-full"
        ) {
            return Err("PG_SSL_MODE is invalid".to_owned());
        }
        let pg_ssl_root_cert = env::var("PG_SSL_ROOT_CERT").ok();
        let pg_ssl_client_cert = env::var("PG_SSL_CLIENT_CERT").ok();
        let pg_ssl_client_key = env::var("PG_SSL_CLIENT_KEY").ok();

        Ok(Self {
            clickhouse_url,
            redis_url,
            pg_max_connections,
            pg_min_connections,
            pg_acquire_timeout,
            scan_interval,
            lock_ttl_secs,
            block_key_ttl_secs,
            storage_report_settlement_enabled,
            pg_ssl_mode,
            pg_ssl_root_cert,
            pg_ssl_client_cert,
            pg_ssl_client_key,
            vault: VaultConfig::from_env()?,
        })
    }
}
