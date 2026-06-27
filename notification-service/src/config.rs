use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub centrifugo_api_url: String,
    pub centrifugo_api_key: String,
    pub redis_url: String,
    // [COMMENT]: Địa chỉ gRPC của ACR service (Rust) — xác thực Trinity Token cho cả User và Admin
    pub acr_grpc_endpoint: String,
    // [COMMENT]: Đường dẫn chứng chỉ CA phục vụ mTLS đến ACR service
    pub acr_grpc_ca_cert: Option<String>,
    // [COMMENT]: Đường dẫn chứng chỉ Client phục vụ mTLS đến ACR service
    pub acr_grpc_client_cert: Option<String>,
    // [COMMENT]: Đường dẫn khóa riêng tư Client phục vụ mTLS đến ACR service
    pub acr_grpc_client_key: Option<String>,
    // [COMMENT]: Domain name cho TLS verification (CN/SAN) khi dùng mTLS đến ACR
    pub acr_grpc_domain: String,
    pub otel_exporter_otlp_endpoint: String,
}

impl Config {
    pub fn from_env() -> Self {
        // [ignoring loop detection]
        // Đọc cấu hình từ biến môi trường với giá trị fallback an toàn
        Self {
            app_port: env::var("APP_PORT")
                .unwrap_or_else(|_| "8083".to_string())
                .parse()
                .expect("APP_PORT must be a valid number"),
            centrifugo_api_url: env::var("CENTRIFUGO_API_URL")
                .unwrap_or_else(|_| "http://centrifugo:8000/api".to_string()),
            centrifugo_api_key: env::var("CENTRIFUGO_API_KEY")
                .unwrap_or_else(|_| "your_centrifugo_api_key_secret".to_string()),
            redis_url: env::var("REDIS_URL")
                .unwrap_or_else(|_| "redis://controlplane-redis-job:6379/0".to_string()),
            // [COMMENT]: Nạp cấu hình ACR gRPC endpoint — mặc định trỏ đến acr:50051 trong Docker network
            acr_grpc_endpoint: env::var("ACR_GRPC_ENDPOINT")
                .or_else(|_| env::var("ACL_GRPC_ENDPOINT"))
                .unwrap_or_else(|_| "acr:50051".to_string()),
            acr_grpc_ca_cert: env::var("ACR_GRPC_CA_CERT")
                .or_else(|_| env::var("ACL_GRPC_CA_CERT")).ok(),
            acr_grpc_client_cert: env::var("ACR_GRPC_CLIENT_CERT")
                .or_else(|_| env::var("ACL_GRPC_CLIENT_CERT")).ok(),
            acr_grpc_client_key: env::var("ACR_GRPC_CLIENT_KEY")
                .or_else(|_| env::var("ACL_GRPC_CLIENT_KEY")).ok(),
            // [COMMENT]: Domain name cho TLS certificate verification khi dùng mTLS
            acr_grpc_domain: env::var("ACR_GRPC_DOMAIN")
                .or_else(|_| env::var("ACL_GRPC_DOMAIN"))
                .unwrap_or_else(|_| "localhost".to_string()),
            otel_exporter_otlp_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string()),
        }
    }
}
