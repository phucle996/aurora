use std::env;

use std::str::FromStr;

/// 🛡️ CHẾ ĐỘ BẢO MẬT TRUYỀN DẪN REDIS (REDIS SECURITY MODE)
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum RedisTlsMode {
    Disable,
    Tls,
    Mtls,
}

impl FromStr for RedisTlsMode {
    type Err = ();
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s.to_ascii_lowercase().as_str() {
            "disable" | "false" => Ok(RedisTlsMode::Disable),
            "tls" | "true" => Ok(RedisTlsMode::Tls),
            "mtls" => Ok(RedisTlsMode::Mtls),
            _ => Ok(RedisTlsMode::Disable),
        }
    }
}

// [COMMENT]: Config chứa JMAP credential; không derive Debug để tránh vô tình ghi secret vào log/panic.
#[derive(Clone)]
pub struct Config {
    /// Định danh phân vùng địa lý (Zone ID) mà Dataplane này được cấp phát để phục vụ.
    /// Ví dụ: "zone-asia-southeast". Dataplane chỉ consume stream tương ứng `jobs:<zone_id>`.
    pub zone_id: String,

    /// Địa chỉ kết nối đến cụm Redis phục vụ Job Queue Stream.
    pub redis_job_url: String,
    /// Chế độ bảo mật TLS cho Job Redis.
    pub redis_job_tls_mode: RedisTlsMode,
    /// Đường dẫn file chứng chỉ CA cho Job Redis.
    pub redis_job_ca_cert: Option<String>,
    /// Đường dẫn file chứng chỉ Client phục vụ mTLS cho Job Redis.
    pub redis_job_client_cert: Option<String>,
    /// Đường dẫn file khóa riêng Client phục vụ mTLS cho Job Redis.
    pub redis_job_client_key: Option<String>,

    /// [COMMENT]: Endpoint JetStream riêng của Zone; tuyệt đối không trỏ sang NATS Core trung tâm.
    pub nats_zone_url: String,
    /// [COMMENT]: Replica factor của Zone KV; production nên dùng 3, dev single-node dùng 1.
    pub nats_zone_kv_replicas: usize,

    /// Endpoint của OpenTelemetry Collector phục vụ gửi traces & metrics.
    pub otel_exporter_otlp_endpoint: String,

    // Cấu hình số lượng Worker tối thiểu chạy ngầm (min concurrency baseline).
    // Đảm bảo hệ thống luôn có ít nhất 1 worker chạy ngầm để tiêu thụ job ngay lập tức, tránh cold start.
    pub min_workers: usize,

    // Cấu hình giới hạn số lượng Worker chạy đồng thời (max concurrency limit).
    // Được nạp tĩnh qua biến môi trường để tối ưu hóa hiệu năng, loại bỏ overengineering của PolicyEngine.
    pub max_workers: usize,

    /// [COMMENT]: Direct JMAP endpoint và service-account binding dùng cho shared bulk-mail client.
    pub stalwart_jmap_url: String,
    pub stalwart_jmap_account_id: String,
    pub stalwart_jmap_identity_id: String,
    pub stalwart_jmap_mailbox_id: String,
    pub stalwart_jmap_bearer_token: String,
    pub stalwart_jmap_username: String,
    pub stalwart_jmap_password: String,
    /// [COMMENT]: Read-only management identity chỉ được cấp ClusterNode query/get; không tái sử dụng mail submission credential.
    pub stalwart_management_jmap_url: String,
    pub stalwart_reporter_bearer_token: String,
    /// [COMMENT]: Sender tĩnh phase Dataplane; Controlplane sender projection sẽ thay registry này ở phase sau.
    pub mail_sender_profile_id: String,
    pub mail_sender_version: u32,
    pub mail_sender_address: String,
    pub mail_batch_max_items: usize,
    pub mail_batch_max_wait_ms: u64,
    pub mail_batch_max_bytes: usize,
    pub mail_batch_queue_capacity: usize,
    pub mail_batch_enqueue_timeout_ms: u64,
    pub mail_jmap_max_inflight_per_pod: usize,
    pub mail_jmap_request_timeout_ms: u64,
    pub mail_jmap_max_retries: usize,
    pub mail_max_message_bytes: usize,

    /// [COMMENT]: Một central Phase-5 scanner đọc bounded NATS KV key page; giới hạn chặn full materialize và thundering herd.
    pub mail_config_scan_interval_seconds: u64,
    pub mail_config_scan_page_size: usize,
    pub mail_config_scan_max_pages_per_tick: usize,
    /// [COMMENT]: Consumer registry có hard cap; template Moka dùng byte-weight thay vì chỉ đếm entry.
    pub mail_consumer_l1_max_entries: usize,
    pub mail_template_l1_max_bytes: u64,
    pub mail_template_l1_ttl_seconds: u64,
    /// [COMMENT]: Phase-6 dùng một supervisor timer chung; lease TTL phải dài hơn nhiều lần chu kỳ reconcile.
    pub mail_stream_supervisor_interval_ms: u64,
    pub mail_stream_slot_lease_ttl_seconds: u64,
    /// [COMMENT]: Mỗi broker suite tự áp backpressure bằng hard cap inflight trên từng claimed slot.
    pub mail_stream_max_inflight_per_slot: usize,
    pub mail_stream_max_slots_per_pod: usize,
    /// [COMMENT]: Phase 7 chỉ được activate khi Phase 8 settlement consumer đã được wire; mặc định false để không gửi rồi mất ACK boundary.
    pub mail_stream_delivery_enabled: bool,
    /// [COMMENT]: Render/JMAP inflight là giới hạn riêng, không nhân nhầm theo số broker connection `parallelism`.
    pub mail_stream_processor_concurrency: usize,
    /// [COMMENT]: Zone-local KEK 32-byte dạng hex; chỉ DP có, CP/JO/Zone KV không được log hoặc giải mã envelope.
    pub mail_stream_envelope_key_hex: String,
    /// [COMMENT]: Optional Kafka Zone CA path là trusted deployment config, không nhận filesystem path từ customer payload.
    pub mail_stream_ca_cert_path: Option<String>,
    /// [COMMENT]: Hai reverse reporter có cadence riêng để consumer cardinality không kéo theo infra load.
    pub mail_consumer_report_interval_ms: u64,
    pub mail_infra_report_interval_ms: u64,

    /// [COMMENT]: Địa chỉ Host kết nối cụm MinIO Cluster cục bộ (Optional)
    pub minio_host: Option<String>,
    /// [COMMENT]: Cổng API dịch vụ MinIO (thường là 9000) (Optional)
    pub minio_port: Option<u16>,

    // ============================================================================
    // 🔒 CẤU HÌNH PROXMOX HYPERVISOR API (Chỉ lưu tại Dataplane — Không lên Controlplane)
    // ============================================================================
    /// Base URL của Proxmox API endpoint (ví dụ: https://pve.example.com:8006)
    /// Credentials này TUYỆT ĐỐI không được đẩy lên Controlplane DB (Blast Radius)
    pub proxmox_api_url: String,
    /// API Token theo định dạng Proxmox: `PVEAPIToken=user@realm!tokenid=<secret>`
    /// Phân quyền tối thiểu (Least Privilege): chỉ cấp quyền đọc node list & metrics
    pub proxmox_api_token: String,
    /// Bỏ qua TLS certificate verification (CHỈ dùng cho môi trường dev/test)
    /// Trên production bắt buộc phải đặt là false để đảm bảo an toàn kết nối
    pub proxmox_tls_insecure: bool,
    /// [COMMENT]: API Endpoint Public phục vụ gọi từ trình duyệt UI (bắt buộc cấu hình)
    pub minio_public_endpoint: String,
}

use std::sync::OnceLock;

static GLOBAL_CONFIG: OnceLock<Config> = OnceLock::new();

impl Config {
    /// Lấy tham chiếu đến cấu hình toàn cục đã được nạp
    pub fn get_global() -> &'static Config {
        GLOBAL_CONFIG
            .get()
            .expect("Config has not been initialized yet")
    }

    /// Đăng ký cấu hình toàn cục khi khởi chạy hệ thống
    pub fn set_global(config: Config) {
        GLOBAL_CONFIG.set(config).ok();
    }

    /// Khởi tạo và nạp toàn bộ cấu hình từ biến môi trường.
    ///
    /// # Luồng Hoạt động (Execution Flow):
    ///   1. Đọc từng khóa cấu hình bằng `std::env::var`.
    ///   2. Nếu thiếu khóa thiết yếu, lập tức gọi abort tiến trình.
    ///   3. Trả về thực thể `Config` hoàn chỉnh.
    pub fn load() -> Self {
        Self {
            // ============================================================================
            // 🚀 CẤU HÌNH CHUNG
            // ============================================================================
            // Zone ID cấu hình - để xử lí đúng stream key tại "jobs:{zone_id}"
            // nếu sai lệch thì sẽ xử lí nhầm zone hoặc không nhận được job
            zone_id: env::var("ZONE_ID").unwrap_or_else(|err| {
                crate::observability::logger::Logger::sys_error(
                    "system.bootstrap",
                    "CRITICAL: ZONE_ID environment variable is missing but required for stateless Dataplane!",
                    &err.to_string(),
                );
                std::process::abort();
            }),

            // [COMMENT]: Nạp cấu hình Endpoint S3 Gateway phục vụ Direct S3 (Bắt buộc cấu hình, không fallback)
            minio_public_endpoint: env::var("MINIO_PUBLIC_ENDPOINT").unwrap_or_else(|err| {
                crate::observability::logger::Logger::sys_error(
                    "system.bootstrap",
                    "CRITICAL: MINIO_PUBLIC_ENDPOINT environment variable is missing but required for S3 Console Direct operations!",
                    &err.to_string(),
                );
                std::process::abort();
            }),

            // ============================================================================
            // 🚀 CẤU HÌNH REDIS JOB QUEUE CLIENT
            // ============================================================================
            redis_job_url: env::var("REDIS_JOB_URL")
                .unwrap_or_else(|_| "redis://controlplane-redis:6379/0".to_string()),
            redis_job_tls_mode: env::var("REDIS_JOB_TLS_ENABLED")
                .unwrap_or_else(|_| "disable".to_string())
                .parse::<RedisTlsMode>()
                .unwrap_or(RedisTlsMode::Disable),
            redis_job_ca_cert: env::var("REDIS_JOB_CA_CERT").ok(),
            redis_job_client_cert: env::var("REDIS_JOB_CLIENT_CERT").ok(),
            redis_job_client_key: env::var("REDIS_JOB_CLIENT_KEY").ok(),

            nats_zone_url: {
                // [COMMENT]: Không fallback NATS_URL vì đó là Core bus trung tâm; cross-wire sẽ phá isolation của Zone.
                let value = env::var("NATS_ZONE_URL").unwrap_or_else(|err| {
                    crate::observability::logger::Logger::sys_error(
                        "system.bootstrap",
                        "CRITICAL: NATS_ZONE_URL is required and must point to the Zone-local JetStream cluster",
                        &err.to_string(),
                    );
                    std::process::abort();
                });
                if value.trim().is_empty() {
                    crate::observability::logger::Logger::sys_error(
                        "system.bootstrap",
                        "CRITICAL: NATS_ZONE_URL cannot be empty",
                        "ZONE_NATS_ENDPOINT_REQUIRED",
                    );
                    std::process::abort();
                }
                for central_variable in ["NATS_URL", "NATS_ADDR"] {
                    if env::var(central_variable)
                        .is_ok_and(|central_url| central_url.trim() == value.trim())
                    {
                        crate::observability::logger::Logger::sys_error(
                            "system.bootstrap",
                            "CRITICAL: Zone JetStream endpoint must not equal the central NATS Core endpoint",
                            "ZONE_NATS_CORE_CROSS_WIRE",
                        );
                        std::process::abort();
                    }
                }
                value
            },
            nats_zone_kv_replicas: parse_env("NATS_ZONE_KV_REPLICAS", 3_usize),
            otel_exporter_otlp_endpoint: env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
                .unwrap_or_else(|_| "http://otel-collector:4317".to_string()),
            // Nạp min_workers từ biến môi trường MIN_WORKERS, mặc định là 1 để giữ tối thiểu 1 worker hoạt động.
            min_workers: env::var("MIN_WORKERS")
                .unwrap_or_else(|_| "1".to_string())
                .parse::<usize>()
                .unwrap_or(1),
            // Nạp max_workers từ biến môi trường MAX_WORKERS, mặc định là 100 nếu không được cấu hình.
            max_workers: env::var("MAX_WORKERS")
                .unwrap_or_else(|_| "100".to_string())
                .parse::<usize>()
                .unwrap_or(100),

            // [COMMENT]: JMAP batch transport; secrets có thể được inject từ Kubernetes Secret/Vault Agent.
            stalwart_jmap_url: env::var("STALWART_JMAP_URL")
                .unwrap_or_else(|_| "http://stalwart-mail:8080/jmap".to_string()),
            // [COMMENT]: Đây là opaque IDs do Stalwart cấp, không đoán từ username/mailbox name.
            stalwart_jmap_account_id: env::var("STALWART_JMAP_ACCOUNT_ID").unwrap_or_default(),
            stalwart_jmap_identity_id: env::var("STALWART_JMAP_IDENTITY_ID").unwrap_or_default(),
            stalwart_jmap_mailbox_id: env::var("STALWART_JMAP_MAILBOX_ID").unwrap_or_default(),
            stalwart_jmap_bearer_token: env::var("STALWART_JMAP_BEARER_TOKEN")
                .unwrap_or_default(),
            stalwart_jmap_username: env::var("STALWART_JMAP_USERNAME")
                .unwrap_or_default(),
            stalwart_jmap_password: env::var("STALWART_JMAP_PASSWORD")
                .unwrap_or_default(),
            stalwart_management_jmap_url: env::var("STALWART_MANAGEMENT_JMAP_URL")
                .unwrap_or_default(),
            stalwart_reporter_bearer_token: env::var("STALWART_REPORTER_BEARER_TOKEN")
                .unwrap_or_default(),
            mail_sender_profile_id: env::var("MAIL_SENDER_PROFILE_ID")
                .unwrap_or_else(|_| "platform-default".to_string()),
            mail_sender_version: parse_env("MAIL_SENDER_VERSION", 1_u32),
            mail_sender_address: env::var("MAIL_SENDER_ADDRESS")
                .unwrap_or_else(|_| "noreply@aurora.system".to_string()),
            mail_batch_max_items: parse_env("MAIL_BATCH_MAX_ITEMS", 50_usize),
            mail_batch_max_wait_ms: parse_env("MAIL_BATCH_MAX_WAIT_MS", 1000_u64),
            mail_batch_max_bytes: parse_env("MAIL_BATCH_MAX_BYTES", 4_194_304_usize),
            mail_batch_queue_capacity: parse_env("MAIL_BATCH_QUEUE_CAPACITY", 5_000_usize),
            mail_batch_enqueue_timeout_ms: parse_env(
                "MAIL_BATCH_ENQUEUE_TIMEOUT_MS",
                1_000_u64,
            ),
            mail_jmap_max_inflight_per_pod: parse_env(
                "MAIL_JMAP_MAX_INFLIGHT_PER_POD",
                4_usize,
            ),
            mail_jmap_request_timeout_ms: parse_env(
                "MAIL_JMAP_REQUEST_TIMEOUT_MS",
                10_000_u64,
            ),
            mail_jmap_max_retries: parse_env("MAIL_JMAP_MAX_RETRIES", 2_usize),
            mail_max_message_bytes: parse_env("MAIL_MAX_MESSAGE_BYTES", 1_048_576_usize),
            mail_config_scan_interval_seconds: parse_env(
                "MAIL_CONFIG_SCAN_INTERVAL_SECONDS",
                60_u64,
            )
            .clamp(5, 3_600),
            mail_config_scan_page_size: parse_env("MAIL_CONFIG_SCAN_PAGE_SIZE", 128_usize)
                .clamp(16, 1_000),
            mail_config_scan_max_pages_per_tick: parse_env(
                "MAIL_CONFIG_SCAN_MAX_PAGES_PER_TICK",
                8_usize,
            )
            .clamp(1, 128),
            mail_consumer_l1_max_entries: parse_env(
                "MAIL_CONSUMER_L1_MAX_ENTRIES",
                50_000_usize,
            )
            .clamp(1_000, 1_000_000),
            mail_template_l1_max_bytes: parse_env(
                "MAIL_TEMPLATE_L1_MAX_BYTES",
                67_108_864_u64,
            )
            .clamp(1_048_576, 1_073_741_824),
            mail_template_l1_ttl_seconds: parse_env(
                "MAIL_TEMPLATE_L1_TTL_SECONDS",
                3_600_u64,
            )
            .clamp(60, 86_400),
            mail_stream_supervisor_interval_ms: parse_env(
                "MAIL_STREAM_SUPERVISOR_INTERVAL_MS",
                1_000_u64,
            )
            .clamp(250, 60_000),
            mail_stream_slot_lease_ttl_seconds: parse_env(
                "MAIL_STREAM_SLOT_LEASE_TTL_SECONDS",
                30_u64,
            )
            .clamp(15, 300),
            mail_stream_max_inflight_per_slot: parse_env(
                "MAIL_STREAM_MAX_INFLIGHT_PER_SLOT",
                256_usize,
            )
            .clamp(16, 10_000),
            mail_stream_max_slots_per_pod: parse_env(
                "MAIL_STREAM_MAX_SLOTS_PER_POD",
                256_usize,
            )
            .clamp(1, 10_000),
            mail_stream_delivery_enabled: env::var("MAIL_STREAM_DELIVERY_ENABLED")
                .is_ok_and(|value| value.eq_ignore_ascii_case("true")),
            mail_stream_processor_concurrency: parse_env(
                "MAIL_STREAM_PROCESSOR_CONCURRENCY",
                64_usize,
            )
            .clamp(1, 1_000),
            mail_stream_envelope_key_hex: env::var("MAIL_STREAM_ENVELOPE_KEY_HEX")
                .unwrap_or_default(),
            mail_stream_ca_cert_path: env::var("MAIL_STREAM_CA_CERT_PATH")
                .ok()
                .filter(|path| !path.trim().is_empty()),
            mail_consumer_report_interval_ms: parse_env(
                "MAIL_CONSUMER_REPORT_INTERVAL_MS",
                5_000_u64,
            )
            .clamp(1_000, 60_000),
            mail_infra_report_interval_ms: parse_env(
                "MAIL_INFRA_REPORT_INTERVAL_MS",
                10_000_u64,
            )
            .clamp(5_000, 120_000),

            // [COMMENT]: Nạp cấu hình MinIO (không có fallback mặc định để hỗ trợ báo trạng thái unknown khi thiếu config)
            minio_host: env::var("MINIO_HOST").ok(),
            minio_port: env::var("MINIO_PORT").ok().and_then(|p| p.parse().ok()),

            // ============================================================================
            // 🔒 CẤU HÌNH PROXMOX HYPERVISOR (Least Privilege API Token — env-only)
            // ============================================================================
            // Base URL Proxmox cluster, mặc định rỗng (Dataplane hoạt động degraded nếu không set)
            proxmox_api_url: env::var("PROXMOX_API_URL")
                .unwrap_or_else(|_| String::new()),
            // Token theo định dạng: PVEAPIToken=monitor@pve!aurora-token=<uuid-secret>
            proxmox_api_token: env::var("PROXMOX_API_TOKEN")
                .unwrap_or_else(|_| String::new()),
            // Chỉ bật trên môi trường dev/staging khi dùng self-signed cert
            proxmox_tls_insecure: env::var("PROXMOX_TLS_INSECURE")
                .map(|v| v.eq_ignore_ascii_case("true") || v == "1")
                .unwrap_or(false),
        }
    }
}

fn parse_env<T>(name: &str, default: T) -> T
where
    T: FromStr,
{
    env::var(name)
        .ok()
        .and_then(|value| value.parse::<T>().ok())
        .unwrap_or(default)
}

/// Trích xuất Hostname tự động tại Bootstrap, fallback sang UUID v4 ngẫu nhiên
pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|h| h.to_string_lossy().into_owned())
            .unwrap_or_else(|_| format!("unknown-{}", uuid::Uuid::new_v4()))
    })
}
