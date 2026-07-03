use std::env;

/// ============================================================================
/// 📂 MODULE: config/mod.rs - Bộ Nạp Cấu Hình Dataplane (aurora-dataplane)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Nạp toàn bộ cấu hình hạ tầng và định danh từ các biến môi trường (.env / system ENV).
///   - Cung cấp một struct `Config` có trạng thái bất biến (immutable) sau khi khởi động.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Môi trường chạy hệ thống (Environment Variables). File `.env` đóng vai trò SoT local
///     trong quá trình phát triển (development). Trên production, SoT được quyết định bởi
///     K8s ConfigMap/Secret hoặc Systemd Environment.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Module này chỉ nạp cấu hình hệ thống cấp thấp (infrastructure-level).
///   - Tuyệt đối KHÔNG chứa hoặc nạp các thông tin cấp độ Tenant (`tenant_id`) hay thông tin
///     nhạy cảm của người dùng cuối. Mọi cấu hình liên quan đến Tenant đều do Controlplane (CP)
///     quản lý và phân lập trước khi phân phối lệnh.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi duy nhất tại điểm bắt đầu (`entry point`) trong `src/main.rs` ngay khi boot:
///     `let cfg = Config::load();`
///   - Thực thể `Config` sau đó sẽ được đóng gói bằng `Arc<Config>` và phân phối (clone reference)
///     cho các module: WorkerPool, JobReceiver, RPC Client/Server, PolicyEngine, Infra Connections.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Nếu thay đổi các biến môi trường này, hệ thống BẮT BUỘC phải thực hiện rolling restart
///     để cấu hình mới có hiệu lực. Nếu muốn cấu hình động không downtime, hãy sử dụng `policyengine/`.
///
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

#[derive(Clone, Debug)]
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

    /// Địa chỉ kết nối đến cụm Redis phục vụ Policy dynamic sync / pub/sub.
    pub redis_internal_zone_url: String,
    /// Chế độ bảo mật TLS cho Internal Zone Redis.
    pub redis_internal_zone_tls_mode: RedisTlsMode,
    /// Đường dẫn file chứng chỉ CA cho Internal Zone Redis.
    pub redis_internal_zone_ca_cert: Option<String>,
    /// Đường dẫn file chứng chỉ Client phục vụ mTLS cho Internal Zone Redis.
    pub redis_internal_zone_client_cert: Option<String>,
    /// Đường dẫn file khóa riêng Client phục vụ mTLS cho Internal Zone Redis.
    pub redis_internal_zone_client_key: Option<String>,

    /// Endpoint của OpenTelemetry Collector phục vụ gửi traces & metrics.
    pub otel_exporter_otlp_endpoint: String,

    // Cấu hình số lượng Worker tối thiểu chạy ngầm (min concurrency baseline).
    // Đảm bảo hệ thống luôn có ít nhất 1 worker chạy ngầm để tiêu thụ job ngay lập tức, tránh cold start.
    pub min_workers: usize,

    // Cấu hình giới hạn số lượng Worker chạy đồng thời (max concurrency limit).
    // Được nạp tĩnh qua biến môi trường để tối ưu hóa hiệu năng, loại bỏ overengineering của PolicyEngine.
    pub max_workers: usize,

    /// Địa chỉ Stalwart host LMTP để gửi mail nội bộ
    pub stalwart_lmtp_host: String,
    /// Cổng kết nối Stalwart LMTP (thường là 24)
    pub stalwart_lmtp_port: u16,

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
}

impl Config {
    /// Khởi tạo và nạp toàn bộ cấu hình từ biến môi trường.
    ///
    /// # Luồng Hoạt động (Execution Flow):
    ///   1. Đọc từng khóa cấu hình bằng `std::env::var`.
    ///   2. Nếu thiếu khóa, tự động áp dụng giá trị mặc định an toàn (safe fallback) phù hợp cho local dev.
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

            redis_internal_zone_url: env::var("REDIS_INTERNAL_ZONE_URL")
                .unwrap_or_else(|_| "redis://controlplane-redis:6379/1".to_string()),
            redis_internal_zone_tls_mode: env::var("REDIS_INTERNAL_ZONE_TLS_ENABLED")
                .unwrap_or_else(|_| "disable".to_string())
                .parse::<RedisTlsMode>()
                .unwrap_or(RedisTlsMode::Disable),
            redis_internal_zone_ca_cert: env::var("REDIS_INTERNAL_ZONE_CA_CERT").ok(),
            redis_internal_zone_client_cert: env::var("REDIS_INTERNAL_ZONE_CLIENT_CERT").ok(),
            redis_internal_zone_client_key: env::var("REDIS_INTERNAL_ZONE_CLIENT_KEY").ok(),
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

            // Nạp thông tin cấu hình gửi nhận email Stalwart LMTP
            stalwart_lmtp_host: env::var("STALWART_LMTP_HOST")
                .unwrap_or_else(|_| "stalwart-mail".to_string()),
            stalwart_lmtp_port: env::var("STALWART_LMTP_PORT")
                .ok()
                .and_then(|p| p.parse::<u16>().ok())
                .unwrap_or(24),

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
                .map(|v| v.to_ascii_lowercase() == "true" || v == "1")
                .unwrap_or(false),
        }
    }
}

/// Trích xuất Hostname tự động tại Bootstrap, fallback sang UUID v4 ngẫu nhiên
pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|h| h.to_string_lossy().into_owned())
            .unwrap_or_else(|_| format!("unknown-{}", uuid::Uuid::new_v4()))
    })
}
