use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::HashMap;

/// ============================================================================
/// 📂 MODULE: policyengine/types.rs - Khai Báo Thực Thể & Luật Kiểm Định Chính Sách
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Khai báo cấu trúc dữ liệu `PolicySet` ánh xạ từ tệp YAML cấu hình chính sách.
///   - Thực hiện tính toán mã kiểm tra (SHA-256 Checksum) của chính sách.
///   - Cung cấp cơ chế tự động kiểm tra tính toàn vẹn và hợp lệ logic (Semantic Validation).
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Tệp tin chính sách dạng YAML đặt trên đĩa cứng hệ thống (File system path).
///   - Struct `PolicySet` là cấu trúc trung gian trước khi nạp vào bộ nhớ động (in-memory).
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Module chỉ xử lý các cấu hình kiểm soát luồng hoạt động (Operational Policy)
///     như giới hạn thread pool, cấu hình retry backoff, log levels hay rate limits.
///   - TUYỆT ĐỐI KHÔNG chứa dữ liệu mật mã thô (plain-text secrets) hoặc thông tin Tenant.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi `policyengine/adapter.rs` sau khi đọc tệp YAML thành công.
///   - Được gọi bởi `policyengine/engine.rs` để chạy bộ hàm `validate()` trước khi
///     tiến hành hoán đổi nguyên tử (atomic swap).
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Mọi bản cập nhật chính sách bắt buộc phải mang mã phiên bản hợp lệ ("v1").
///   - Thuật toán băm SHA-256 được chạy cục bộ trên mỗi node để so sánh checksum. Nếu checksum trùng,
///     hệ thống sẽ drop event ngay lập tức nhằm chống lãng phí CPU (parse payload).
///
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct PolicySet {
    /// Phiên bản thiết kế của cấu trúc chính sách. Hiện tại chỉ hỗ trợ cứng: "v1".
    pub version: String,

    /// Mốc thời gian cập nhật bản ghi chính sách này (Định dạng RFC3339 UTC).
    pub updated_at: String,

    /// Đường dẫn file vật lý hoặc định danh nguồn của tệp cấu hình chính sách này.
    pub source: String,

    /// Mã băm kiểm tra tính toàn vẹn của tệp tin nguồn (SHA-256 Checksum).
    pub checksum_sha: String,

    /// Bản đồ chứa toàn bộ cặp giá trị chính sách động.
    /// Ví dụ: {"max_workers": 100, "rate_limit_rps": 500}
    pub policies: HashMap<String, serde_yaml::Value>,
}

impl PolicySet {
    /// Tính toán mã kiểm tra SHA-256 từ chuỗi YAML thô để làm định danh duy nhất (Idempotent Checksum).
    ///
    /// # Luồng xử lý:
    ///   - Đọc bytes thô -> Cập nhật hash digest -> Tạo chuỗi thập lục phân (hex string).
    pub fn calculate_checksum(raw_yaml: &str) -> String {
        let mut hasher = Sha256::new();
        hasher.update(raw_yaml.as_bytes());
        format!("{:x}", hasher.finalize())
    }

    /// Bộ kiểm định logic (Semantic & Syntactic Validation) bảo vệ hệ thống khỏi crash runtime.
    ///
    /// # Luật Bất Biến (Invariants Checking):
    ///   - `version` phải bằng "v1". Nếu sai, từ chối nạp để tránh lỗi tương thích ngược.
    ///   - Hộp chứa chính sách `policies` bắt buộc phải có phần tử, cấm rỗng (empty map check).
    ///
    /// # Trạng thái lỗi (Error Fallback):
    ///   - Trả về `Err(String)` chứa thông báo lỗi chi tiết để hệ thống ghi nhận cảnh báo
    ///     và tiếp tục duy trì trạng thái chính sách cũ hoạt động tốt (Last-Known-Good).
    pub fn validate(&self) -> Result<(), String> {
        if self.version != "v1" {
            return Err("Unsupported policy version. Only 'v1' is allowed.".to_string());
        }
        if self.policies.is_empty() {
            return Err("Policies map cannot be empty.".to_string());
        }
        Ok(())
    }
}
