use crate::error::AclError;
use std::env;
use std::time::Duration;

/// Lấy hostname của node/container hiện tại (dùng cho OTel resource attributes)
pub fn get_node_hostname() -> String {
    env::var("HOSTNAME")
        .or_else(|_| env::var("NODE_NAME"))
        .unwrap_or_else(|_| "acl-unknown".to_string())
}

#[derive(Debug, Clone)]
pub struct VaultConfig {
    pub addr: String,
    pub token: String,
    pub role_id: String,
    pub secret_id: String,
    pub transit_key_name: String,
    pub timeout: Duration,
    pub max_retries: usize,
}

// Cấu trúc chứa toàn bộ biến môi trường của dịch vụ ACL
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
    // [COMMENT]: Địa chỉ kết nối gRPC đến Control Plane (mặc định localhost:9443)
    pub controlplane_grpc_endpoint: String,
    // [COMMENT]: Đường dẫn chứng chỉ CA tự ký phục vụ xác thực HTTPS/gRPC
    pub controlplane_grpc_ca_cert: Option<String>,
    // [COMMENT]: Đường dẫn chứng chỉ Client phục vụ xác thực mTLS hai chiều
    pub controlplane_grpc_client_cert: Option<String>,
    // [COMMENT]: Đường dẫn khóa riêng tư của Client phục vụ mTLS
    pub controlplane_grpc_client_key: Option<String>,
    // [COMMENT]: Danh sách các endpoint được phép bypass không cần kiểm tra token
    pub bypass_endpoints: Vec<String>,
    // [COMMENT]: Domain công khai của hệ thống để gắn kết session cookie (đọc từ APP_PUBLIC_DOMAIN)
    pub app_public_domain: String,
}

impl Config {
    // Load cấu hình từ môi trường hệ thống
    pub fn from_env() -> Result<Self, AclError> {
        // Tải các biến môi trường từ file .env nếu có
        let _ = dotenvy::dotenv();

        let grpc_port = env::var("ACL_GRPC_PORT")
            .unwrap_or_else(|_| "50051".to_string())
            .parse::<u16>()
            .map_err(|_| {
                AclError::ConfigError("ACL_GRPC_PORT must be a valid port number".to_string())
            })?;

        let redis_url =
            env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());

        // Vault configurations
        let vault_addr =
            env::var("VAULT_ADDR").unwrap_or_else(|_| "http://127.0.0.1:8200".to_string());

        let vault_token = env::var("VAULT_TOKEN").unwrap_or_else(|_| "".to_string());

        let vault_role_id = env::var("VAULT_ROLE_ID").unwrap_or_else(|_| "".to_string());

        let vault_secret_id = env::var("VAULT_SECRET_ID").unwrap_or_else(|_| "".to_string());

        let vault_transit_key_name =
            env::var("VAULT_TRANSIT_KEY_NAME").unwrap_or_else(|_| "jwt-signer".to_string());

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
            transit_key_name: vault_transit_key_name,
            timeout: Duration::from_secs(vault_timeout_secs),
            max_retries: vault_max_retries,
        };

        let session_ttl_secs = env::var("SESSION_TTL_SECS")
            .unwrap_or_else(|_| "1800".to_string())
            .parse::<u64>()
            .map_err(|_| AclError::ConfigError("SESSION_TTL_SECS must be a number".to_string()))?;

        let refresh_threshold_secs = env::var("REFRESH_THRESHOLD_SECS")
            .unwrap_or_else(|_| "900".to_string())
            .parse::<u64>()
            .map_err(|_| {
                AclError::ConfigError("REFRESH_THRESHOLD_SECS must be a number".to_string())
            })?;

        // Endpoint OTel Collector (mặc định trỏ đến sidecar trong cùng Pod K8s)
        let otel_exporter_otlp_endpoint = env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://otel-collector:4317".to_string());

        // [COMMENT]: Khai báo nạp biến môi trường cho gRPC client đi đến Controlplane
        let controlplane_grpc_endpoint =
            env::var("CONTROLPLANE_GRPC_ENDPOINT").unwrap_or_else(|_| "localhost:9443".to_string());
        let controlplane_grpc_ca_cert = env::var("CONTROLPLANE_GRPC_CA_CERT").ok();
        let controlplane_grpc_client_cert = env::var("CONTROLPLANE_GRPC_CLIENT_CERT").ok();
        let controlplane_grpc_client_key = env::var("CONTROLPLANE_GRPC_CLIENT_KEY").ok();

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
                    "/api/v1/auth/login".to_string(),
                    "/api/v1/health".to_string(),
                ]
            });

        // [COMMENT]: Nạp domain công khai từ biến môi trường APP_PUBLIC_DOMAIN
        let app_public_domain = env::var("APP_PUBLIC_DOMAIN").unwrap_or_default();

        Ok(Config {
            grpc_port,
            redis_url,
            vault,
            session_ttl_secs,
            refresh_threshold_secs,
            otel_exporter_otlp_endpoint,
            controlplane_grpc_endpoint,
            controlplane_grpc_ca_cert,
            controlplane_grpc_client_cert,
            controlplane_grpc_client_key,
            bypass_endpoints,
            app_public_domain,
        })
    }
}
