use std::env;
use std::path::PathBuf;
use std::time::Duration;

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
    /// URL kết nối tới PostgreSQL (billing database)
    pub database_url: String,
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

    // --- CẤU HÌNH BẢO MẬT KẾT NỐI (TLS/mTLS) ---
    /// PostgreSQL SSL Mode (`disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full`)
    pub pg_ssl_mode: String,
    /// Đường dẫn tới file CA Certificate để xác thực PostgreSQL Server Cert
    pub pg_ssl_root_cert: Option<String>,
    /// Đường dẫn tới file Client Certificate dùng cho mTLS PostgreSQL
    pub pg_ssl_client_cert: Option<String>,
    /// Đường dẫn tới file Client Private Key dùng cho mTLS PostgreSQL
    pub pg_ssl_client_key: Option<String>,

    /// Đường dẫn tới file CA Certificate để xác thực ClickHouse Server Cert (HTTPS)
    pub ch_ssl_root_cert: Option<String>,
    /// Đường dẫn tới file Client Certificate dùng cho mTLS ClickHouse
    pub ch_ssl_client_cert: Option<String>,
    /// Đường dẫn tới file Client Private Key dùng cho mTLS ClickHouse
    pub ch_ssl_client_key: Option<String>,

    /// Đường dẫn tới file CA Certificate để xác thực Redis Server Cert (rediss://)
    pub redis_ssl_root_cert: Option<String>,
    /// Đường dẫn tới file Client Certificate dùng cho mTLS Redis
    pub redis_ssl_client_cert: Option<String>,
    /// Đường dẫn tới file Client Private Key dùng cho mTLS Redis
    pub redis_ssl_client_key: Option<String>,
    pub vault: VaultConfig,
}

impl VaultConfig {
    fn from_env() -> Self {
        Self {
            addr: env::var("VAULT_ADDR").unwrap_or_else(|_| "http://127.0.0.1:8200".to_owned()),
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
        }
    }
}

impl Config {
    /// Đọc các cấu hình từ biến môi trường và gán giá trị mặc định nếu thiếu
    pub fn from_env() -> Self {
        // Connection URLs are resolved by infra connectors from Vault.
        let database_url = String::new();

        // Đọc ClickHouse URL, mặc định http://clickhouse:8123
        let clickhouse_url =
            env::var("CLICKHOUSE_URL").unwrap_or_else(|_| "http://clickhouse:8123".to_string());

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

        // --- Đọc cấu hình TLS/mTLS từ biến môi trường ---

        let pg_ssl_mode = env::var("PG_SSL_MODE").unwrap_or_else(|_| "prefer".to_string());
        let pg_ssl_root_cert = env::var("PG_SSL_ROOT_CERT").ok();
        let pg_ssl_client_cert = env::var("PG_SSL_CLIENT_CERT").ok();
        let pg_ssl_client_key = env::var("PG_SSL_CLIENT_KEY").ok();

        let ch_ssl_root_cert = env::var("CH_SSL_ROOT_CERT").ok();
        let ch_ssl_client_cert = env::var("CH_SSL_CLIENT_CERT").ok();
        let ch_ssl_client_key = env::var("CH_SSL_CLIENT_KEY").ok();

        let redis_ssl_root_cert = env::var("REDIS_SSL_ROOT_CERT").ok();
        let redis_ssl_client_cert = env::var("REDIS_SSL_CLIENT_CERT").ok();
        let redis_ssl_client_key = env::var("REDIS_SSL_CLIENT_KEY").ok();

        Self {
            database_url,
            clickhouse_url,
            redis_url,
            pg_max_connections,
            pg_min_connections,
            pg_acquire_timeout,
            scan_interval,
            lock_ttl_secs,
            block_key_ttl_secs,
            pg_ssl_mode,
            pg_ssl_root_cert,
            pg_ssl_client_cert,
            pg_ssl_client_key,
            ch_ssl_root_cert,
            ch_ssl_client_cert,
            ch_ssl_client_key,
            redis_ssl_root_cert,
            redis_ssl_client_cert,
            redis_ssl_client_key,
            vault: VaultConfig::from_env(),
        }
    }
}
