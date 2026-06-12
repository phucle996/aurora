use std::env;

/// Config lưu giữ các tham số cấu hình kết nối của job-proxy.
#[derive(Clone, Debug)]
pub struct Config {
    /// Chuỗi kết nối Postgres (phải bật wal_level = logical)
    pub database_url: String,

    /// Chuỗi kết nối Redis (đẩy job vào Stream)
    pub redis_url: String,

    /// Tên Logical Replication Slot đã tạo trong Postgres
    pub slot_name: String,

    /// Tên Publication đã đăng ký trong Postgres
    pub publication_name: String,

    /// Tên Stream nhận kết quả xử lý từ Dataplane
    pub result_stream_name: String,
}

impl Config {
    /// Đọc các tham số cấu hình từ biến môi trường
    pub fn from_env() -> Result<Self, String> {
        // Load file .env nếu có
        let _ = dotenvy::dotenv();

        let database_url = env::var("DATABASE_URL")
            .map_err(|_| "DATABASE_URL must be set".to_string())?;

        let redis_url = env::var("REDIS_URL")
            .map_err(|_| "REDIS_URL must be set".to_string())?;

        let slot_name = env::var("REPLICATION_SLOT_NAME")
            .unwrap_or_else(|_| "outbox_slot".to_string());

        let publication_name = env::var("PUBLICATION_NAME")
            .unwrap_or_else(|_| "outbox_pub".to_string());

        let result_stream_name = env::var("RESULT_STREAM_NAME")
            .unwrap_or_else(|_| "job_results_stream".to_string());

        Ok(Self {
            database_url,
            redis_url,
            slot_name,
            publication_name,
            result_stream_name,
        })
    }
}

