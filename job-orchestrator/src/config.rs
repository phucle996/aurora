use std::env;

/// Config lưu giữ các tham số cấu hình kết nối của job-proxy.
// Config contains database, Redis, Kafka and NATS credentials. Deliberately do not derive Debug:
// an accidental `{:?}` during bootstrap must never serialize the complete secret-bearing struct.
#[derive(Clone)]
pub struct Config {
    /// Chuỗi kết nối Postgres (phải bật wal_level = logical)
    pub database_url: String,

    /// [COMMENT]: Shared Redis chỉ giữ Central watch/report bridge,
    /// bounded streams và reconciler lock/checkpoint; không phải Zone runtime database.
    pub shared_redis_url: String,

    /// [COMMENT]: Kafka trung tâm là durable transport cho job/result/metadata/report.
    pub kafka_bootstrap_servers: String,
    pub kafka_security_protocol: String,
    pub kafka_username: Option<String>,
    pub kafka_password: Option<String>,
    pub kafka_ca_cert: Option<String>,
    pub kafka_topic_prefix: String,

    /// Tên Logical Replication Slot đã tạo trong Postgres
    pub slot_name: String,

    /// Tên Publication đã đăng ký trong Postgres
    pub publication_name: String,

    /// Cấu hình OpenTelemetry exporter (gửi traces/metrics đến OTel Collector)
    pub env_nats_url: String,
    pub otel_exporter_otlp_endpoint: String,
    /// Root trace ratio; incoming W3C parent decisions remain authoritative.
    pub otel_trace_sample_ratio: f64,
    pub otel_metric_export_interval_secs: u64,
    pub otel_export_timeout_secs: u64,
    /// Danh sách schema.table mà logical changefeed theo dõi.
    pub changefeed_sources: Vec<String>,
    /// Số lần thử lại tối đa khi thiết lập hạ tầng Logical Replication trước khi tắt ứng dụng
    pub changefeed_bootstrap_attempts: u32,

    /// [COMMENT]: Central mail reconciler chạy batch nhỏ; không tạo ticker riêng cho từng Zone.
    pub mail_reconcile_interval_secs: u64,
    pub mail_reconcile_scheduler_tick_secs: u64,
    pub mail_reconcile_jitter_max_ms: u64,
    pub mail_reconcile_lock_ttl_secs: u64,
    pub mail_reconcile_page_size: i64,
    pub mail_reconcile_max_pages_per_run: usize,
    pub mail_reconcile_work_budget_secs: u64,

    /// [COMMENT]: Runtime report là Redis soft state có TTL, chỉ tồn tại trong watch window;
    /// consumer reclaim entry thật sự bị pod khác bỏ lại và không polling PostgreSQL theo chu kỳ.
    pub mail_runtime_report_ttl_secs: u64,
    pub mail_runtime_report_claim_idle_ms: u64,

    /// [COMMENT]: Ownership là durable Central-internal delivery. PostgreSQL
    /// storage outbox giữ recovery marker; Shared Redis chỉ là bounded transport.
    pub ownership_reconcile_interval_secs: u64,
    pub ownership_reconcile_batch_size: i64,
    pub ownership_lease_secs: u64,
    pub ownership_stream_capacity: usize,
    pub shared_redis_aof_replica_acks: i64,
    pub shared_redis_aof_timeout_ms: u64,
}

impl Config {
    /// Đọc các tham số cấu hình từ biến môi trường
    pub fn from_env() -> Result<Self, String> {
        // Load file .env nếu có
        let _ = dotenvy::dotenv();

        let database_url =
            env::var("DATABASE_URL").map_err(|_| "DATABASE_URL must be set".to_string())?;

        let shared_redis_url =
            env::var("SHARED_REDIS_URL").map_err(|_| "SHARED_REDIS_URL must be set".to_string())?;
        let kafka_bootstrap_servers = env::var("KAFKA_BOOTSTRAP_SERVERS")
            .map_err(|_| "KAFKA_BOOTSTRAP_SERVERS must be set".to_string())?;
        let kafka_security_protocol = env::var("KAFKA_SECURITY_PROTOCOL")
            .unwrap_or_else(|_| "plaintext".to_string())
            .to_ascii_lowercase();
        let kafka_username = env::var("KAFKA_USERNAME").ok();
        let kafka_password = env::var("KAFKA_PASSWORD").ok();
        let kafka_ca_cert = env::var("KAFKA_CA_CERT").ok();
        let kafka_topic_prefix =
            env::var("KAFKA_TOPIC_PREFIX").unwrap_or_else(|_| "aurora".to_string());

        let slot_name =
            env::var("REPLICATION_SLOT_NAME").unwrap_or_else(|_| "outbox_slot".to_string());

        let publication_name =
            env::var("PUBLICATION_NAME").unwrap_or_else(|_| "outbox_pub".to_string());

        // [COMMENT]: Đọc NATS_URL từ biến môi trường bắt buộc (Fail-fast, không fallback URL hạ tầng)
        let env_nats_url = env::var("NATS_URL").map_err(|_| "NATS_URL must be set".to_string())?;

        // Đọc endpoint của OpenTelemetry Collector (mặc định trỏ tới otel-collector trên cổng 4317)
        let otel_exporter_otlp_endpoint = env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://controlplane-otel-collector:4317".to_string());
        let configured_trace_sample_ratio = env::var("OTEL_TRACE_SAMPLE_RATIO")
            .ok()
            .and_then(|value| value.parse::<f64>().ok())
            .unwrap_or(1.0);
        let otel_trace_sample_ratio = if configured_trace_sample_ratio.is_finite() {
            configured_trace_sample_ratio.clamp(0.0, 1.0)
        } else {
            1.0
        };
        let otel_metric_export_interval_secs = env::var("OTEL_METRIC_EXPORT_INTERVAL_SECS")
            .ok()
            .and_then(|value| value.parse::<u64>().ok())
            .unwrap_or(15)
            .clamp(5, 300);
        let otel_export_timeout_secs = env::var("OTEL_EXPORT_TIMEOUT_SECS")
            .ok()
            .and_then(|value| value.parse::<u64>().ok())
            .unwrap_or(10)
            .clamp(1, 30)
            .min(otel_metric_export_interval_secs);

        // Keep CDC_SOURCES as a rolling-compatible environment alias.
        let changefeed_sources_raw = env::var("CDC_SOURCES")
            .unwrap_or_else(|_| "mail.mail_outbox_records,storage.storage_outbox_records,hierarchy.zones,hierarchy.zone_services".to_string());
        let changefeed_sources = changefeed_sources_raw
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect::<Vec<String>>();

        // Đọc giới hạn số lần retry khi setup hạ tầng replication
        let changefeed_bootstrap_attempts = env::var("MAX_SETUP_RETRIES")
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
        let mail_runtime_report_ttl_secs = env::var("MAIL_RUNTIME_REPORT_TTL_SECS")
            .unwrap_or_else(|_| "45".to_string())
            .parse::<u64>()
            .unwrap_or(45)
            .clamp(30, 120);
        let mail_runtime_report_claim_idle_ms = env::var("MAIL_RUNTIME_REPORT_CLAIM_IDLE_MS")
            .unwrap_or_else(|_| "30000".to_string())
            .parse::<u64>()
            .unwrap_or(30_000)
            .clamp(5_000, 300_000);
        let ownership_reconcile_interval_secs = env::var("OWNERSHIP_RECONCILE_INTERVAL_SECS")
            .unwrap_or_else(|_| "30".to_string())
            .parse::<u64>()
            .unwrap_or(30)
            .clamp(5, 300);
        let ownership_reconcile_batch_size = env::var("OWNERSHIP_RECONCILE_BATCH_SIZE")
            .unwrap_or_else(|_| "50".to_string())
            .parse::<i64>()
            .unwrap_or(50)
            .clamp(1, 500);
        let ownership_lease_secs = env::var("OWNERSHIP_LEASE_SECS")
            .unwrap_or_else(|_| "30".to_string())
            .parse::<u64>()
            .unwrap_or(30)
            .clamp(10, 300);
        let ownership_stream_capacity = env::var("OWNERSHIP_STREAM_CAPACITY")
            .unwrap_or_else(|_| "100000".to_string())
            .parse::<usize>()
            .map_err(|_| "OWNERSHIP_STREAM_CAPACITY must be an integer".to_string())?;
        if !(1_000..=10_000_000).contains(&ownership_stream_capacity) {
            return Err("OWNERSHIP_STREAM_CAPACITY must be between 1000 and 10000000".to_string());
        }
        // Production defaults to one replica durability ACK. Docker/single-node
        // must explicitly override to 0 rather than silently weakening prod.
        let shared_redis_aof_replica_acks = env::var("SHARED_REDIS_AOF_REPLICA_ACKS")
            .unwrap_or_else(|_| "1".to_string())
            .parse::<i64>()
            .map_err(|_| "SHARED_REDIS_AOF_REPLICA_ACKS must be an integer".to_string())?;
        if !(0..=5).contains(&shared_redis_aof_replica_acks) {
            return Err("SHARED_REDIS_AOF_REPLICA_ACKS must be between 0 and 5".to_string());
        }
        let shared_redis_aof_timeout_ms = env::var("SHARED_REDIS_AOF_TIMEOUT_MS")
            .unwrap_or_else(|_| "2000".to_string())
            .parse::<u64>()
            .map_err(|_| "SHARED_REDIS_AOF_TIMEOUT_MS must be an integer".to_string())?;
        if !(100..=10_000).contains(&shared_redis_aof_timeout_ms) {
            return Err("SHARED_REDIS_AOF_TIMEOUT_MS must be between 100 and 10000".to_string());
        }
        Ok(Self {
            database_url,
            shared_redis_url,
            kafka_bootstrap_servers,
            kafka_security_protocol,
            kafka_username,
            kafka_password,
            kafka_ca_cert,
            kafka_topic_prefix,
            slot_name,
            publication_name,
            env_nats_url,
            otel_exporter_otlp_endpoint,
            otel_trace_sample_ratio,
            otel_metric_export_interval_secs,
            otel_export_timeout_secs,
            changefeed_sources,
            changefeed_bootstrap_attempts,
            mail_reconcile_interval_secs,
            mail_reconcile_scheduler_tick_secs,
            mail_reconcile_jitter_max_ms,
            mail_reconcile_lock_ttl_secs,
            mail_reconcile_page_size,
            mail_reconcile_max_pages_per_run,
            mail_reconcile_work_budget_secs,
            mail_runtime_report_ttl_secs,
            mail_runtime_report_claim_idle_ms,
            ownership_reconcile_interval_secs,
            ownership_reconcile_batch_size,
            ownership_lease_secs,
            ownership_stream_capacity,
            shared_redis_aof_replica_acks,
            shared_redis_aof_timeout_ms,
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
