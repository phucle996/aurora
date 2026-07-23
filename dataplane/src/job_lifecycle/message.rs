use serde::{Deserialize, Serialize};

/// ============================================================================
/// 📂 MODULE: job-receiver/message.rs - Đặc Tả Cấu Trúc Message & Phân Tích Dữ Liệu
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Khai báo cấu trúc dữ liệu `JobPayload` đại diện cho các Job nghiệp vụ được đẩy vào Dataplane.
///   - Ánh xạ Protobuf `JobCommandV1` lấy từ Kafka sang entity thực thi nội bộ.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Message được đẩy vào Kafka topic theo Zone hoặc Platform bởi Job Orchestrator.
///
/// 🔒 RANH GIỚI BẢO MẬT NGHIÊM NGẶT (CRITICAL PRIVACY BOUNDARY):
///   - TUYỆT ĐỐI CẤM chứa trường `tenant_id` hoặc bất kỳ dữ liệu định danh khách hàng nào trực tiếp trong Payload.
///   - Dataplane chỉ xử lý các thông số kỹ thuật thô phục vụ ảo hóa/hạ tầng (ví dụ: ram, cpu, disk).
///   - Ranh giới này đảm bảo Dataplane tuân thủ tuyệt đối chuẩn bảo mật, không có thông tin định danh người dùng.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi `job_lifecycle/consumer.rs` sau khi decode Protobuf Kafka.
///   - Được truyền vào `executor/mod.rs` để thực thi nghiệp vụ cụ thể.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Trường `trace_id` là bắt buộc để liên kết vết xử lý (trace context propagation) từ Controlplane
///     qua Dataplane, hỗ trợ SRE truy vết lỗi xuyên suốt hệ thống cực nhanh.
///
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobPayload {
    /// Mã định danh duy nhất của Job nghiệp vụ.
    pub job_id: String,

    /// Phiên bản của Job (dùng để kiểm soát xung đột ghi đè dữ liệu - Optimistic Concurrency Control).
    pub job_version: u32,

    /// Số lần thử lại hiện tại của Job này (Retry Count).
    pub attempt: u32,

    /// Chủ đề/Loại công việc cần xử lý. Dùng để định tuyến động (Dynamic Routing).
    /// Ví dụ: "vps.create", "vps.resize", "mail.send".
    pub job_topic: String,

    /// Domain sở hữu outbox nguồn: IAM | MAIL | STORAGE.
    pub source_domain: String,

    /// Mã tài nguyên hạ tầng chịu tác động trực tiếp của Job này.
    /// Ví dụ: "vps-uuid-12345".
    pub resource_id: String,

    /// Phiên bản thiết kế của cấu trúc payload bên dưới.
    pub payload_schema_version: u32,

    /// Dữ liệu kỹ thuật chi tiết phục vụ thực thi nghiệp vụ dưới dạng nhị phân.
    pub payload: Vec<u8>,

    /// Mã định danh vết xử lý xuyên suốt hệ thống (Distributed Trace Context).
    pub trace_id: String,

    /// Hạn mức thời gian chạy tối đa (giây) của công việc
    pub idle: Option<u32>,

    /// [COMMENT]: Chỉ có ở DB-backed reconciliation command; live WAL job để None.
    #[serde(default)]
    pub reconcile_generation: Option<u64>,

    /// [COMMENT]: Zone đích được ký trong envelope; consumer Zone fail-close nếu không khớp Zone hiện tại.
    #[serde(default)]
    pub target_zone_id: String,

    /// [COMMENT]: Kafka delivery metadata chỉ sinh lúc consume, không được serialize vào domain payload.
    #[serde(skip)]
    pub kafka_delivery: Option<crate::infra::kafka::KafkaDelivery>,

    /// [COMMENT]: Lease runtime chỉ sinh sau khi consume; không phải field của Kafka envelope.
    #[serde(skip)]
    pub zone_lease: Option<crate::infra::zone_kv::ZoneLease>,
}
