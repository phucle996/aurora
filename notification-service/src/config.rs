use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub centrifugo_api_url: String,
    pub centrifugo_api_key: String,
    pub redis_url: String,
    // [COMMENT]: Địa chỉ gRPC của ACL service (Rust) — xác thực Trinity Token cho cả User và Admin
    pub acl_grpc_endpoint: String,
    // [COMMENT]: Đường dẫn chứng chỉ CA phục vụ mTLS đến ACL service
    pub acl_grpc_ca_cert: Option<String>,
    // [COMMENT]: Đường dẫn chứng chỉ Client phục vụ mTLS đến ACL service
    pub acl_grpc_client_cert: Option<String>,
    // [COMMENT]: Đường dẫn khóa riêng tư Client phục vụ mTLS đến ACL service
    pub acl_grpc_client_key: Option<String>,
    // [COMMENT]: Domain name cho TLS verification (CN/SAN) khi dùng mTLS đến ACL
    pub acl_grpc_domain: String,
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
            // [COMMENT]: Nạp cấu hình ACL gRPC endpoint — mặc định trỏ đến acl:50051 trong Docker network
            acl_grpc_endpoint: env::var("ACL_GRPC_ENDPOINT")
                .unwrap_or_else(|_| "acl:50051".to_string()),
            acl_grpc_ca_cert: env::var("ACL_GRPC_CA_CERT").ok(),
            acl_grpc_client_cert: env::var("ACL_GRPC_CLIENT_CERT").ok(),
            acl_grpc_client_key: env::var("ACL_GRPC_CLIENT_KEY").ok(),
            // [COMMENT]: Domain name cho TLS certificate verification khi dùng mTLS
            acl_grpc_domain: env::var("ACL_GRPC_DOMAIN")
                .unwrap_or_else(|_| "localhost".to_string()),
            otel_exporter_otlp_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string()),
        }
    }
}
