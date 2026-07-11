use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub centrifugo_api_url: String,
    pub centrifugo_api_key: String,
    // [COMMENT]: Địa chỉ NATS Core
    pub nats_url: String,
    // [COMMENT]: Đường dẫn chứng chỉ CA phục vụ TLS/mTLS đến NATS Core
    pub nats_ca_cert: Option<String>,
    // [COMMENT]: Đường dẫn chứng chỉ Client phục vụ mTLS đến NATS Core
    pub nats_client_cert: Option<String>,
    // [COMMENT]: Đường dẫn khóa riêng tư Client phục vụ mTLS đến NATS Core
    pub nats_client_key: Option<String>,
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
            // [COMMENT]: Nạp cấu hình kết nối NATS Core
            nats_url: env::var("NATS_ADDR")
                .unwrap_or_else(|_| "nats://nats:4222".to_string()),
            nats_ca_cert: env::var("NATS_CA_CERT").ok(),
            nats_client_cert: env::var("NATS_CLIENT_CERT").ok(),
            nats_client_key: env::var("NATS_CLIENT_KEY").ok(),
            otel_exporter_otlp_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string()),
        }
    }
}
