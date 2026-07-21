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

    /// Cấu hình OpenTelemetry exporter (gửi traces/metrics đến OTel Collector)
    pub env_nats_url: String,
    pub otel_exporter_otlp_endpoint: String,
    /// Định danh vùng (zone_id) để đánh nhãn metrics/traces
    pub zone_id: String,
    /// Danh sách các bảng outbox cần theo dõi CDC (ví dụ: mail.mail_outbox_records)
    pub cdc_sources: Vec<String>,
    /// Số lần thử lại tối đa khi thiết lập hạ tầng Logical Replication trước khi tắt ứng dụng
    pub max_setup_retries: u32,

    /// [COMMENT]: Central mail reconciler chạy batch nhỏ; không tạo ticker riêng cho từng Zone.
    pub mail_reconcile_interval_secs: u64,
    pub mail_reconcile_scheduler_tick_secs: u64,
    pub mail_reconcile_jitter_max_ms: u64,
    pub mail_reconcile_lock_ttl_secs: u64,
    pub mail_reconcile_page_size: i64,
    pub mail_reconcile_max_pages_per_run: usize,
    pub mail_reconcile_work_budget_secs: u64,
}

impl Config {
    /// Đọc các tham số cấu hình từ biến môi trường
    pub fn from_env() -> Result<Self, String> {
        // Load file .env nếu có
        let _ = dotenvy::dotenv();

        let database_url =
            env::var("DATABASE_URL").map_err(|_| "DATABASE_URL must be set".to_string())?;

        let redis_url = env::var("REDIS_URL").map_err(|_| "REDIS_URL must be set".to_string())?;

        let slot_name =
            env::var("REPLICATION_SLOT_NAME").unwrap_or_else(|_| "outbox_slot".to_string());

        let publication_name =
            env::var("PUBLICATION_NAME").unwrap_or_else(|_| "outbox_pub".to_string());

        let result_stream_name =
            env::var("RESULT_STREAM_NAME").unwrap_or_else(|_| "job_results_stream".to_string());

        // Đọc nats_url từ biến môi trường
        let env_nats_url =
            env::var("NATS_URL").unwrap_or_else(|_| "nats://controlplane-nats:4222".to_string());

        // Đọc endpoint của OpenTelemetry Collector (mặc định trỏ tới otel-collector trên cổng 4317)
        let otel_exporter_otlp_endpoint = env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string());

        // Đọc zone_id từ biến môi trường (mặc định là unknown)
        let zone_id = env::var("ZONE_ID").unwrap_or_else(|_| "unknown".to_string());

        // Đọc danh sách các bảng CDC phân cách bởi dấu phẩy
        let cdc_sources_raw = env::var("CDC_SOURCES")
            .unwrap_or_else(|_| "mail.mail_outbox_records,iam.iam_outbox_records,storage.storage_outbox_records,hierarchy.zones,hierarchy.zone_services".to_string());
        let cdc_sources = cdc_sources_raw
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect::<Vec<String>>();

        // Đọc giới hạn số lần retry khi setup hạ tầng replication
        let max_setup_retries = env::var("MAX_SETUP_RETRIES")
            .unwrap_or_else(|_| "10".to_string())
            .parse::<u32>()
            .unwrap_or(10);

        let mail_reconcile_interval_secs = env::var("MAIL_RECONCILE_INTERVAL_SECS")
            .unwrap_or_else(|_| "600".to_string())
            .parse::<u64>()
            .unwrap_or(600)
            .max(60);
        let mail_reconcile_scheduler_tick_secs = env::var("MAIL_RECONCILE_SCHEDULER_TICK_SECS")
            .unwrap_or_else(|_| "5".to_string())
            .parse::<u64>()
            .unwrap_or(5)
            .clamp(2, 60);
        let mail_reconcile_jitter_max_ms = env::var("MAIL_RECONCILE_JITTER_MAX_MS")
            .unwrap_or_else(|_| "30000".to_string())
            .parse::<u64>()
            .unwrap_or(30_000)
            .max(1_000);
        let mail_reconcile_lock_ttl_secs = env::var("MAIL_RECONCILE_LOCK_TTL_SECS")
            .unwrap_or_else(|_| "60".to_string())
            .parse::<u64>()
            .unwrap_or(60)
            .max(30);
        let mail_reconcile_page_size = env::var("MAIL_RECONCILE_PAGE_SIZE")
            .unwrap_or_else(|_| "100".to_string())
            .parse::<i64>()
            .unwrap_or(100)
            .clamp(10, 500);
        let mail_reconcile_max_pages_per_run = env::var("MAIL_RECONCILE_MAX_PAGES_PER_RUN")
            .unwrap_or_else(|_| "4".to_string())
            .parse::<usize>()
            .unwrap_or(4)
            .clamp(1, 32);
        let mail_reconcile_work_budget_secs = env::var("MAIL_RECONCILE_WORK_BUDGET_SECS")
            .unwrap_or_else(|_| "20".to_string())
            .parse::<u64>()
            .unwrap_or(20)
            .clamp(5, mail_reconcile_lock_ttl_secs.saturating_sub(5));

        Ok(Self {
            database_url,
            redis_url,
            slot_name,
            publication_name,
            result_stream_name,
            env_nats_url,
            otel_exporter_otlp_endpoint,
            zone_id,
            cdc_sources,
            max_setup_retries,
            mail_reconcile_interval_secs,
            mail_reconcile_scheduler_tick_secs,
            mail_reconcile_jitter_max_ms,
            mail_reconcile_lock_ttl_secs,
            mail_reconcile_page_size,
            mail_reconcile_max_pages_per_run,
            mail_reconcile_work_budget_secs,
        })
    }
}

/// Hàm phụ trợ lấy Hostname của node hiện tại phục vụ định danh tài nguyên (Resource Attributes)
pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|h| h.into_string().unwrap_or_default())
            .unwrap_or_default()
    })
}
