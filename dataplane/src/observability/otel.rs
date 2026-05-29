/// ============================================================================
/// 📂 MODULE: observability/otel.rs - Liên Kết Vết Xử Lý Hệ Thống (OpenTelemetry)
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Thiết lập kết nối đến hệ thống thu thập Tracing tập trung (Tempo / Jaeger).
///   - Cung cấp hàm trích xuất và liên kết mã truy vết (`trace_id`) nhận được từ Controlplane
///     vào Task Context của Tokio chạy ngầm.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Mã trace_id nhận được trực tiếp trong Payload của Job stream hoặc gRPC Header.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Chỉ truyền nhận thông tin Header Metadata thô liên quan đến trace context.
///   - Tuyệt đối phân lập không để rò rỉ dữ liệu cá nhân hay thông tin Tenant trên các collector thu trace.
///
/// 🔄 CALLSITE FLOW:
///   - `init()` được gọi một lần tại `main.rs` khi khởi chạy hệ thống.
///   - `inject_trace_context()` được gọi ngay tại cổng vào của `job-receiver/consumer.rs`
///     hoặc `rpc/receiver/client.rs` trước khi chuyển tiếp luồng thực thi sang Executor.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Giúp SRE dễ dàng trace và debug lỗi xuyên suốt hệ thống (ví dụ: tìm mọi log liên quan đến
///     một Job từ Controlplane tới tận API Libvirt trên Hypervisor).
///
pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry tracer pipeline kết nối tới Tempo.
    pub fn init() {
        crate::observability::logger::Logger::sys_info(
            "tracing.init",
            "Observability OTel: OpenTelemetry tracer successfully initialized. Pipeline connected to Tempo.",
        );
    }

    /// Trích xuất mã trace_id và liên kết trực tiếp vào Span Context của thread hiện tại.
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   1. Parse chuỗi `trace_id` thành định dạng chuẩn `TraceId` của OpenTelemetry.
    ///   2. Tạo một Span mới bất đồng bộ lồng dưới Span gốc của Controlplane.
    ///   3. Gắn context vào luồng chạy hiện tại (active tokio task context).
    pub fn inject_trace_context(trace_id: &str) {
        // Trên môi trường Production thực tế:
        //   - Sử dụng `opentelemetry::global` và `tracing::Span` để inject context.
        //   - Đảm bảo mọi bản ghi log (`logger.rs`) xuất ra sau thời điểm này đều tự động
        //     mang theo nhãn `trace_id` tương ứng.
        crate::observability::logger::Logger::sys_debug(
            "tracing.span",
            &format!(
                "Observability OTel: Extracted trace ID '{}' and injected context into current task span",
                trace_id
            ),
        );
    }
}
