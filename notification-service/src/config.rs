use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub app_port: u16,
    pub centrifugo_api_url: String,
    pub centrifugo_api_key: String,
    pub redis_url: String,
    pub controlplane_grpc_endpoint: String,
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
                .unwrap_or_else(|_| "controlplane-dev-1:9090".to_string()),
        }
    }
}
