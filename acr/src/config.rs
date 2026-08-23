use crate::error::AcrError;
use std::env;
use std::time::Duration;

fn required_env(name: &str) -> Result<String, AcrError> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .ok_or_else(|| AcrError::ConfigError(format!("{name} is required")))
}

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
    pub kubernetes_role: String,
    pub kubernetes_jwt_path: String,
    pub transit_key_path: String,
    pub totp_key_path: String,
    pub admin_api_key_path: String,
    // Dedicated asymmetric Transit key for the private Central-to-Zone control
    // boundary. Missing infrastructure configuration fails ACR startup.
    pub zone_control_assertion_key_path: String,
    pub zone_control_assertion_key_id: String,
    pub timeout: Duration,
    pub max_retries: usize,
}

// Cấu trúc chứa toàn bộ biến môi trường của dịch vụ ACR
#[derive(Debug, Clone)]
pub struct Config {
    // Port lắng nghe gRPC server; deployment phải khai báo rõ để tránh bind nhầm.
    pub grpc_port: u16,
    // Cấu hình kết nối Vault phục vụ signing, TOTP, OAuth và Redis bootstrap.
    pub vault: VaultConfig,
    // Thời gian sống tối đa của Access Session (mặc định: 1800 giây - 30 phút)
    pub session_ttl_secs: u64,
    // Ngưỡng kích hoạt Trinity Refresh (mặc định: 900 giây - 15 phút)
    pub refresh_threshold_secs: u64,
    // Redis topology is infrastructure-only. Compose uses a single node while
    // Kubernetes uses Redis Cluster; business workflows do not branch on it.
    pub auth_state_redis_mode: RedisMode,
    pub shared_l2_redis_mode: RedisMode,
    // Địa chỉ kết nối OTLP Collector (gRPC endpoint cho Tracing + Metrics)
    pub otel_exporter_otlp_endpoint: String,
    // [COMMENT]: Danh sách các endpoint được phép bypass không cần kiểm tra token
    pub bypass_endpoints: Vec<String>,
    // [COMMENT]: Domain công khai của hệ thống để gắn kết session cookie (đọc từ APP_PUBLIC_DOMAIN)
    pub app_public_domain: String,
    // [COMMENT]: Origin đích duy nhất cho one-time Billing handoff; code không được redirect theo input client.
    pub billing_console_origin: String,
    // [COMMENT]: Danh sách các origin được phép gọi API (đọc từ APP_ALLOWED_ORIGINS)
    pub allowed_origins: Vec<String>,
    pub oauth: OAuthConfig,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RedisMode {
    Single,
    Cluster,
}

fn redis_mode(name: &str) -> Result<RedisMode, AcrError> {
    match env::var(name)
        .unwrap_or_else(|_| "single".to_string())
        .trim()
        .to_ascii_lowercase()
        .as_str()
    {
        "single" => Ok(RedisMode::Single),
        "cluster" => Ok(RedisMode::Cluster),
        _ => Err(AcrError::ConfigError(format!(
            "{name} must be either single or cluster"
        ))),
    }
}

#[derive(Debug, Clone)]
pub struct OAuthProviderConfig {
    pub enabled: bool,
    pub client_id: String,
    pub callback_url: String,
    pub scope: String,
}

#[derive(Debug, Clone)]
pub struct OAuthConfig {
    pub google: OAuthProviderConfig,
    pub github: OAuthProviderConfig,
}

impl OAuthProviderConfig {
    fn from_env(provider: &str) -> Self {
        let provider_name = provider.to_ascii_uppercase();
        let enabled = env::var(format!("OAUTH_{provider_name}_ENABLED"))
            .ok()
            .map(|value| {
                matches!(
                    value.trim().to_ascii_lowercase().as_str(),
                    "1" | "true" | "yes"
                )
            })
            .unwrap_or(false);
        Self {
            enabled,
            // All provider configuration below is a runtime slot. When enabled,
            // OAuthProviderService fills it from the fixed Vault record for this
            // provider; no credential, callback URL, or scope comes from env.
            client_id: String::new(),
            callback_url: String::new(),
            scope: String::new(),
        }
    }
}

impl Config {
    // Load cấu hình từ môi trường hệ thống
    pub fn from_env() -> Result<Self, AcrError> {
        // Tải các biến môi trường từ file .env nếu có
        let _ = dotenvy::dotenv();

        let grpc_port = required_env("ACR_GRPC_PORT")?.parse::<u16>().map_err(|_| {
            AcrError::ConfigError("ACR_GRPC_PORT must be a valid port number".to_string())
        })?;

        // Vault configurations
        // Vault is the source of signing, TOTP, OAuth and connection identity.
        // Falling back to a local endpoint can silently bind ACR to the wrong
        // security authority, so the deployment must state it explicitly.
        let vault_addr = required_env("VAULT_ADDR")?;

        let vault_token = env::var("VAULT_TOKEN").unwrap_or_else(|_| "".to_string());

        let vault_role_id = env::var("VAULT_ROLE_ID").unwrap_or_else(|_| "".to_string());

        let vault_secret_id = env::var("VAULT_SECRET_ID").unwrap_or_else(|_| "".to_string());
        let vault_kubernetes_role =
            env::var("VAULT_KUBERNETES_ROLE").unwrap_or_else(|_| "".to_string());
        let vault_kubernetes_jwt_path = env::var("VAULT_KUBERNETES_JWT_PATH")
            .unwrap_or_else(|_| "/var/run/secrets/kubernetes.io/serviceaccount/token".to_string());

        let vault_transit_key_path = required_env("VAULT_TRANSIT_KEY_PATH")?;
        let vault_totp_key_path = required_env("VAULT_TOTP_KEY_PATH")?;
        let vault_admin_api_key_path = required_env("VAULT_ADMIN_API_KEY_PATH")?;

        let vault_zone_control_assertion_key_path =
            env::var("VAULT_ZONE_CONTROL_ASSERTION_KEY_PATH")
                .ok()
                .filter(|value| !value.trim().is_empty())
                .ok_or_else(|| {
                    AcrError::ConfigError(
                        "VAULT_ZONE_CONTROL_ASSERTION_KEY_PATH is required".to_string(),
                    )
                })?;
        let vault_zone_control_assertion_key_id = env::var("VAULT_ZONE_CONTROL_ASSERTION_KEY_ID")
            .ok()
            .filter(|value| !value.trim().is_empty())
            .ok_or_else(|| {
                AcrError::ConfigError("VAULT_ZONE_CONTROL_ASSERTION_KEY_ID is required".to_string())
            })?;

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
            kubernetes_role: vault_kubernetes_role,
            kubernetes_jwt_path: vault_kubernetes_jwt_path,
            transit_key_path: vault_transit_key_path,
            totp_key_path: vault_totp_key_path,
            admin_api_key_path: vault_admin_api_key_path,
            zone_control_assertion_key_path: vault_zone_control_assertion_key_path,
            zone_control_assertion_key_id: vault_zone_control_assertion_key_id,
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
        let auth_state_redis_mode = redis_mode("AUTH_STATE_REDIS_MODE")?;
        let shared_l2_redis_mode = redis_mode("SHARED_L2_REDIS_MODE")?;

        // Endpoint OTel Collector (mặc định trỏ đến sidecar trong cùng Pod K8s)
        let otel_exporter_otlp_endpoint = env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://otel-collector:4317".to_string());

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
                    // Payment providers have no browser session. Cost Manager verifies an HMAC
                    // over the exact body and persists an idempotent webhook inbox.
                    "POST /api/v1/billing/webhooks/personal/payment-settled".to_string(),
                    "POST /api/v1/billing/webhooks/tenant/payment-settled".to_string(),
                ]
            });

        // [COMMENT]: Enforce host-only cookies by keeping app_public_domain empty to prevent cookie sharing across subdomains
        let app_public_domain = String::new();

        let billing_console_origin = required_env("BILLING_CONSOLE_ORIGIN")?
            .trim_end_matches('/')
            .to_string();
        if !(billing_console_origin.starts_with("https://")
            || billing_console_origin.starts_with("http://localhost:"))
        {
            return Err(AcrError::ConfigError(
                "BILLING_CONSOLE_ORIGIN must use https (localhost http is allowed for development)"
                    .to_string(),
            ));
        }

        // [COMMENT]: Nạp danh sách các domain/origin được phép truy cập từ biến môi trường APP_ALLOWED_ORIGINS
        let allowed_origins = required_env("APP_ALLOWED_ORIGINS")?
            .split(',')
            .map(|item| item.trim().to_string())
            .filter(|item| !item.is_empty())
            .collect::<Vec<String>>();
        if allowed_origins.is_empty() {
            return Err(AcrError::ConfigError(
                "APP_ALLOWED_ORIGINS must contain at least one origin".to_string(),
            ));
        }

        let oauth = OAuthConfig {
            google: OAuthProviderConfig::from_env("google"),
            github: OAuthProviderConfig::from_env("github"),
        };

        Ok(Config {
            grpc_port,
            vault,
            session_ttl_secs,
            refresh_threshold_secs,
            auth_state_redis_mode,
            shared_l2_redis_mode,
            otel_exporter_otlp_endpoint,
            bypass_endpoints,
            app_public_domain,
            billing_console_origin,
            allowed_origins,
            oauth,
        })
    }
}
