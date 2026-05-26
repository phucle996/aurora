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
#[derive(Clone, Debug)]
pub struct Config {
    /// Định danh phân vùng địa lý (Zone ID) mà Dataplane này được cấp phát để phục vụ.
    /// Ví dụ: "zone-asia-southeast". Dataplane chỉ consume stream tương ứng `jobs:<zone_id>`.
    pub zone_id: String,

    /// Địa chỉ kết nối đến cụm Redis phục vụ Job Queue Stream.
    pub redis_job_url: String,
    /// Trạng thái kích hoạt TLS cho Job Redis.
    pub redis_job_tls_enabled: bool,
    /// Đường dẫn file chứng chỉ CA cho Job Redis.
    pub redis_job_ca_cert: Option<String>,
    /// Đường dẫn file chứng chỉ Client phục vụ mTLS cho Job Redis.
    pub redis_job_client_cert: Option<String>,
    /// Đường dẫn file khóa riêng Client phục vụ mTLS cho Job Redis.
    pub redis_job_client_key: Option<String>,

    /// Địa chỉ kết nối đến cụm Redis phục vụ Policy dynamic sync / pub/sub.
    pub redis_policy_url: String,
    /// Trạng thái kích hoạt TLS cho Policy Redis.
    pub redis_policy_tls_enabled: bool,
    /// Đường dẫn file chứng chỉ CA cho Policy Redis.
    pub redis_policy_ca_cert: Option<String>,
    /// Đường dẫn file chứng chỉ Client phục vụ mTLS cho Policy Redis.
    pub redis_policy_client_cert: Option<String>,
    /// Đường dẫn file khóa riêng Client phục vụ mTLS cho Policy Redis.
    pub redis_policy_client_key: Option<String>,

    /// Địa chỉ gRPC endpoint của Controlplane để gửi các báo cáo hoàn thành nghiệp vụ hoặc lazy state.
    /// Định dạng: "host:port"
    pub controlplane_grpc_host: String,

    /// Cấu hình mức độ hiển thị nhật ký hệ thống (log level).
    /// Giá trị: "debug" | "info" | "warn" | "error"
    pub log_level: String,
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
            zone_id: env::var("ZONE_ID").unwrap_or_else(|_| "zone-asia-southeast".to_string()),

            redis_job_url: env::var("REDIS_JOB_URL").unwrap_or_else(|_| "redis://controlplane-redis:6379/0".to_string()),
            redis_job_tls_enabled: env::var("REDIS_JOB_TLS_ENABLED")
                .unwrap_or_else(|_| "false".to_string())
                .parse::<bool>()
                .unwrap_or(false),
            redis_job_ca_cert: env::var("REDIS_JOB_CA_CERT").ok(),
            redis_job_client_cert: env::var("REDIS_JOB_CLIENT_CERT").ok(),
            redis_job_client_key: env::var("REDIS_JOB_CLIENT_KEY").ok(),

            redis_policy_url: env::var("REDIS_POLICY_URL").unwrap_or_else(|_| "redis://controlplane-redis:6379/1".to_string()),
            redis_policy_tls_enabled: env::var("REDIS_POLICY_TLS_ENABLED")
                .unwrap_or_else(|_| "false".to_string())
                .parse::<bool>()
                .unwrap_or(false),
            redis_policy_ca_cert: env::var("REDIS_POLICY_CA_CERT").ok(),
            redis_policy_client_cert: env::var("REDIS_POLICY_CLIENT_CERT").ok(),
            redis_policy_client_key: env::var("REDIS_POLICY_CLIENT_KEY").ok(),

            controlplane_grpc_host: env::var("CONTROLPLANE_GRPC_HOST").unwrap_or_else(|_| "controlplane-dev-1:9090".to_string()),
            log_level: env::var("APP_LOG_LEVEL").unwrap_or_else(|_| "info".to_string()),
        }
    }
}
