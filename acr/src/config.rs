use crate::error::AcrError;
use std::env;
use std::time::Duration;

/// Lấy hostname của node/container hiện tại (dùng cho OTel resource attributes)
pub fn get_node_hostname() -> String {
    env::var("HOSTNAME")
        .or_else(|_| env::var("NODE_NAME"))
        .unwrap_or_else(|_| "acr-unknown".to_string())
}

#[derive(Debug, Clone)]
pub struct VaultConfig {
    pub addr: String,
    pub token: String,
    pub role_id: String,
    pub secret_id: String,
    pub transit_key_path: String,
    pub totp_key_path: String,
    pub admin_api_key_path: String,
    pub timeout: Duration,
    pub max_retries: usize,
}

// Cấu trúc chứa toàn bộ biến môi trường của dịch vụ ACR
#[derive(Debug, Clone)]
pub struct Config {
    // Port lắng nghe gRPC server (mặc định: 50051)
    pub grpc_port: u16,
    // Địa chỉ kết nối cụm Redis L2
    pub redis_url: String,
    // Cấu hình kết nối Vault phục vụ việc xác thực JWT
    pub vault: VaultConfig,
    // Thời gian sống tối đa của Access Session (mặc định: 1800 giây - 30 phút)
    pub session_ttl_secs: u64,
    // Ngưỡng kích hoạt Trinity Refresh (mặc định: 900 giây - 15 phút)
    pub refresh_threshold_secs: u64,
    // Địa chỉ kết nối OTLP Collector (gRPC endpoint cho Tracing + Metrics)
    pub otel_exporter_otlp_endpoint: String,
    // [COMMENT]: Danh sách các endpoint được phép bypass không cần kiểm tra token
    pub bypass_endpoints: Vec<String>,
    // [COMMENT]: Domain công khai của hệ thống để gắn kết session cookie (đọc từ APP_PUBLIC_DOMAIN)
    pub app_public_domain: String,
    // [COMMENT]: Danh sách các origin được phép gọi API (đọc từ APP_ALLOWED_ORIGINS)
    pub allowed_origins: Vec<String>,
    // [COMMENT]: Địa chỉ kết nối NATS Core Client
    pub nats_url: String,
    // [COMMENT]: Đường dẫn CA cert phục vụ kết nối TLS/mTLS cho NATS
    pub nats_ca_cert: Option<String>,
    // [COMMENT]: Đường dẫn Client cert phục vụ kết nối TLS/mTLS cho NATS
    pub nats_client_cert: Option<String>,
    // [COMMENT]: Đường dẫn Client private key phục vụ kết nối TLS/mTLS cho NATS
    pub nats_client_key: Option<String>,
}

impl Config {
    // Load cấu hình từ môi trường hệ thống
    pub fn from_env() -> Result<Self, AcrError> {
        // Tải các biến môi trường từ file .env nếu có
        let _ = dotenvy::dotenv();

        let grpc_port = env::var("ACR_GRPC_PORT")
            .or_else(|_| env::var("ACR_GRPC_PORT"))
            .unwrap_or_else(|_| "50051".to_string())
            .parse::<u16>()
            .map_err(|_| {
                AcrError::ConfigError("ACR_GRPC_PORT must be a valid port number".to_string())
            })?;

        let redis_url =
            env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());

        // Vault configurations
        let vault_addr =
            env::var("VAULT_ADDR").unwrap_or_else(|_| "http://127.0.0.1:8200".to_string());

        let vault_token = env::var("VAULT_TOKEN").unwrap_or_else(|_| "".to_string());

        let vault_role_id = env::var("VAULT_ROLE_ID").unwrap_or_else(|_| "".to_string());

        let vault_secret_id = env::var("VAULT_SECRET_ID").unwrap_or_else(|_| "".to_string());

        let vault_transit_key_path = env::var("VAULT_TRANSIT_KEY_PATH")
            .unwrap_or_else(|_| "transit/keys/jwt-signer".to_string());

        let vault_totp_key_path =
            env::var("VAULT_TOTP_KEY_PATH").unwrap_or_else(|_| "totp/keys/admin".to_string());

        let vault_admin_api_key_path = env::var("VAULT_ADMIN_API_KEY_PATH")
            .unwrap_or_else(|_| "secret/data/admin/api-key".to_string());

        let vault_timeout_secs = env::var("VAULT_TIMEOUT_SECS")
            .unwrap_or_else(|_| "5".to_string())
            .parse::<u64>()
            .unwrap_or(5);

        let vault_max_retries = env::var("VAULT_MAX_RETRIES")
            .unwrap_or_else(|_| "3".to_string())
            .parse::<usize>()
            .unwrap_or(3);

        let vault = VaultConfig {
            addr: vault_addr,
            token: vault_token,
            role_id: vault_role_id,
            secret_id: vault_secret_id,
            transit_key_path: vault_transit_key_path,
            totp_key_path: vault_totp_key_path,
            admin_api_key_path: vault_admin_api_key_path,
            timeout: Duration::from_secs(vault_timeout_secs),
            max_retries: vault_max_retries,
        };

        let session_ttl_secs = env::var("SESSION_TTL_SECS")
            .unwrap_or_else(|_| "1800".to_string())
            .parse::<u64>()
            .map_err(|_| AcrError::ConfigError("SESSION_TTL_SECS must be a number".to_string()))?;

        let refresh_threshold_secs = env::var("REFRESH_THRESHOLD_SECS")
            .unwrap_or_else(|_| "900".to_string())
            .parse::<u64>()
            .map_err(|_| {
                AcrError::ConfigError("REFRESH_THRESHOLD_SECS must be a number".to_string())
            })?;

        // Endpoint OTel Collector (mặc định trỏ đến sidecar trong cùng Pod K8s)
        let otel_exporter_otlp_endpoint = env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://otel-collector:4317".to_string());

        let nats_url = env::var("NATS_ADDR").unwrap_or_else(|_| "nats://nats:4222".to_string());
        let nats_ca_cert = env::var("NATS_CA_CERT").ok();
        let nats_client_cert = env::var("NATS_CLIENT_CERT").ok();
        let nats_client_key = env::var("NATS_CLIENT_KEY").ok();

        // [COMMENT]: Nạp danh sách bypass endpoints từ biến môi trường BYPASS_ENDPOINTS (phân tách bởi dấu phẩy)
        let bypass_endpoints = env::var("BYPASS_ENDPOINTS")
            .map(|s| {
                s.split(',')
                    .map(|item| item.trim().to_string())
                    .filter(|item| !item.is_empty())
                    .collect::<Vec<String>>()
            })
            .unwrap_or_else(|_| {
                vec![
                    "GET /api/v1/health/liveness".to_string(),
                    "GET /api/v1/health/readiness".to_string(),
                    "GET /api/v1/health/startup".to_string(),
                    "POST /api/v1/auth/register".to_string(),
                    "POST /api/v1/auth/verify".to_string(),
                ]
            });

        // [COMMENT]: Enforce host-only cookies by keeping app_public_domain empty to prevent cookie sharing across subdomains
        let app_public_domain = String::new();

        // [COMMENT]: Nạp danh sách các domain/origin được phép truy cập từ biến môi trường APP_ALLOWED_ORIGINS
        let allowed_origins = env::var("APP_ALLOWED_ORIGINS")
            .map(|s| {
                s.split(',')
                    .map(|item| item.trim().to_string())
                    .filter(|item| !item.is_empty())
                    .collect::<Vec<String>>()
            })
            .unwrap_or_default();

        Ok(Config {
            grpc_port,
            redis_url,
            vault,
            session_ttl_secs,
            refresh_threshold_secs,
            otel_exporter_otlp_endpoint,
            bypass_endpoints,
            app_public_domain,
            allowed_origins,
            nats_url,
            nats_ca_cert,
            nats_client_cert,
            nats_client_key,
        })
    }
}
