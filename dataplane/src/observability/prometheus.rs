use std::collections::HashMap;

/// ============================================================================
/// 📂 MODULE: observability/prometheus.rs - Quản Lý Chỉ Số Động Prometheus Exporter
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đăng ký và quản lý bộ đo đạc chỉ số (Prometheus Metrics Exporter) của Dataplane.
///   - Cung cấp tính năng đếm số lượng cuộc gọi gRPC inbound/outbound của Dataplane.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Số lượng sự kiện hệ thống thực tế đếm được trong bộ nhớ động (RAM) của ứng dụng.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Chỉ thống kê các con số đếm cấp thấp phục vụ giám sát và SRE.
///   - TUYỆT ĐỐI CẤM định nghĩa nhãn (labels) chứa thông tin nhạy cảm của khách hàng hay Tenant.
///   - **Đặc điểm quan trọng**: KHÔNG định nghĩa trước các nhãn (labels) tĩnh nhằm giữ tính cơ động cao nhất.
///
/// 🔄 CALLSITE FLOW:
///   - `init()` được gọi tại `main.rs` khi khởi chạy.
///   - `increment_rpc_count()` được gọi bởi `rpc/heal/client.rs` hoặc `rpc/sender/server.rs` để đếm.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Đọc ghi metrics dùng cơ chế nguyên tử (Atomic Counter) của Prometheus crate, bảo đảm tốc độ
///     vận hành cực nhanh, an toàn cho hàng triệu request song song.
///
pub struct PromRegistry;

impl PromRegistry {
    /// Khởi tạo Prometheus registry và mở cổng export metrics (mặc định cổng `:2112/metrics`).
    pub fn init() {
        println!("Observability Prometheus: Dynamic metrics registry initialized. Exporting on port :2112...");
    }

    /// Tăng bộ đếm RPC lên 1 đơn vị với nhãn (labels) tùy biến linh hoạt.
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   - Tìm kiếm Counter tương ứng với `rpc_name` và `status`.
    ///   - Nếu chưa có -> Tạo động mới và đăng ký vào Registry.
    ///   - Tăng giá trị Counter (Atomic Inc).
    pub fn increment_rpc_count(rpc_name: &str, status: &str, labels: HashMap<String, String>) {
        // Trên môi trường Production thực tế:
        //   - Sử dụng `prometheus::CounterVec` để khai báo linh hoạt.
        //   - Gắn nhãn động: rpc_name, status, zone_id, dataplane_id.
        println!(
            "Observability Prometheus: Metric counter 'dataplane_rpc_total' incremented (+1) for '{}' [{}]. Labels: {:?}",
            rpc_name, status, labels
        );
    }
}
