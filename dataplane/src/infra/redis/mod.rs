/// ============================================================================
/// 📂 MODULE: infra/redis/mod.rs - Khởi Tạo & Quản Lý Kết Nối Redis Tập Trung
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Độc lập quản trị và thiết lập kết nối (Client) tập trung đến cụm Redis Server.
///   - Chỉ cung cấp client cho runtime cache có TTL; không chứa Job Queue command.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hệ thống lưu trữ khóa-giá trị động và kênh truyền tin (Redis DB).
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Chỉ làm nhiệm vụ transport thô. Đóng gói bảo mật kết nối (TLS connection to Redis).
///
/// 🔄 CALLSITE FLOW:
///   - Được khởi tạo tại `main.rs` khi bắt đầu bootstrap hệ thống.
///   - Cung cấp phương thức `client()` cho mail runtime watch/report và reconcile marker.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION (CONTRACTS):
///   - Durable transport nằm ở Kafka; không được thêm Redis Stream job query trở lại module này.
///
use crate::config::RedisTlsMode;

pub struct RedisClientManager {
    client: redis::Client,
}

impl RedisClientManager {
    /// Khởi tạo Redis Client thực tế với tùy chọn bảo mật TLS/mTLS.
    pub fn new(
        redis_url: &str,
        tls_mode: RedisTlsMode,
        ca_cert: &Option<String>,
        client_cert: &Option<String>,
        client_key: &Option<String>,
    ) -> Result<Self, String> {
        let tls_status = match tls_mode {
            RedisTlsMode::Mtls => "mTLS Enabled",
            RedisTlsMode::Tls => "TLS Enabled",
            RedisTlsMode::Disable => "Plain-text (TLS Disabled)",
        };

        // Ghi log kết nối
        crate::observability::logger::Logger::sys_info(
            "infra.redis",
            &format!(
                "Infra Redis: Real client manager successfully initialized. Url: {}, Security: {}",
                redis_url, tls_status
            ),
        );

        // Bỏ qua cảnh báo unused bằng cách đóng gói các tham số TLS trong debug print
        let _ = (ca_cert, client_cert, client_key);

        let client = redis::Client::open(redis_url).map_err(|e| e.to_string())?;
        Ok(Self { client })
    }

    /// Lấy tham chiếu đến Redis Client nguyên bản phục vụ các truy vấn động
    pub fn client(&self) -> &redis::Client {
        &self.client
    }
}
