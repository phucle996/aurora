use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub centrifugo_api_url: String,
    pub centrifugo_api_key: String,
    pub redis_url: String,
    pub controlplane_grpc_endpoint: String,
    // Đường dẫn chứng chỉ CA phục vụ xác thực HTTPS/gRPC
    pub controlplane_grpc_ca_cert: Option<String>,
    // Đường dẫn chứng chỉ Client phục vụ xác thực hai chiều mTLS
    pub controlplane_grpc_client_cert: Option<String>,
    // Đường dẫn khóa riêng tư của Client phục vụ mTLS
    pub controlplane_grpc_client_key: Option<String>,
    pub otel_exporter_otlp_endpoint: String,
    pub zone_id: String,
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
            controlplane_grpc_endpoint: env::var("CONTROLPLANE_GRPC_ENDPOINT")
                .unwrap_or_else(|_| "controlplane-dev-1:9443".to_string()), // Cập nhật port chuẩn sang 9443
            controlplane_grpc_ca_cert: env::var("CONTROLPLANE_GRPC_CA_CERT").ok(),
            controlplane_grpc_client_cert: env::var("CONTROLPLANE_GRPC_CLIENT_CERT").ok(),
            controlplane_grpc_client_key: env::var("CONTROLPLANE_GRPC_CLIENT_KEY").ok(),
            otel_exporter_otlp_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string()),
            zone_id: env::var("ZONE_ID").unwrap_or_else(|_| "zone-global".to_string()),
        }
    }
}
