use std::time::Duration;
use reqwest::{Client, Response};

/// [COMMENT]: MinioClient quản lý các kết nối HTTP tới cụm MinIO Cluster nội vùng (Local Zone).
pub struct MinioClient {
    client: Client,
    host: String,
    port: u16,
}

impl MinioClient {
    /// Khởi tạo MinIO Client từ Host và Port cụ thể
    pub fn new(host: String, port: u16) -> Self {
        let client = Client::builder()
            .timeout(Duration::from_secs(3))
            .build()
            .unwrap_or_else(|_| Client::new());

        Self { client, host, port }
    }

    /// Khởi tạo MinIO Client tự động đọc cấu hình từ biến môi trường
    pub fn from_env() -> Self {
        let host = std::env::var("MINIO_HOST").unwrap_or_else(|_| "localhost".to_string());
        let port_str = std::env::var("MINIO_PORT").unwrap_or_else(|_| "9000".to_string());
        let port = port_str.parse::<u16>().unwrap_or(9000);

        Self::new(host, port)
    }

    /// Gửi yêu cầu HTTP PUT khởi tạo bucket vật lý lên MinIO
    pub async fn create_bucket(&self, bucket_name: &str) -> Result<Response, reqwest::Error> {
        let url = format!("http://{}:{}/{}", self.host, self.port, bucket_name);
        self.client.put(&url).send().await
    }
}
