/// ============================================================================
/// 📂 MODULE: workerpool/metrics.rs - Quản Lý Chỉ Số Hiệu Năng Worker
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Cung cấp cơ cấu dữ liệu `MetricsType` đại diện cho các thông số vận hành của worker.
///   - Cho phép các module gọi đo đạc một cách linh hoạt (optional & dynamic metrics).
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Các số liệu thống kê runtime thu được trực tiếp tại luồng xử lý của worker.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Chỉ đo đạc hiệu năng hạ tầng (lag, latency, connection counts).
///   - TUYỆT ĐỐI KHÔNG ghi nhận thông tin nhạy cảm của khách hàng hay Tenant ID vào nhãn đo đạc.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi các worker khi hoàn thành một Job (`HandlerLatencyMs`).
///   - Được gọi bởi trình giám sát Stream (`RedisStreamLag`) để cấp số liệu cho `auto_scale.rs`.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Việc thu thập metrics được thiết kế bất đồng bộ để tránh gây nghẽn (non-blocking) luồng xử lý chính.
///
#[derive(Clone, Debug)]
pub enum MetricsType {
    /// Độ lệch (lag) giữa vị trí đọc hiện tại và đầu cuối của Redis Stream.
    RedisStreamLag(u64),
    
    /// Thời gian xử lý thực tế của một Job nghiệp vụ (tính bằng mili-giây).
    HandlerLatencyMs(f64),
    
    /// Số lượng kết nối mạng/luồng xử lý đang hoạt động đồng thời.
    ActiveConnectionsCount(usize),
}

pub struct WorkerMetricsManager;

impl WorkerMetricsManager {
    /// Báo cáo và ghi nhận số liệu đo đạc dựa theo loại chỉ số được truyền vào.
    ///
    /// # Tham số linh hoạt:
    ///   - Cho phép caller tự do quyết định loại metrics cần đo tại runtime để tối ưu hiệu năng.
    pub fn record_metrics(metrics: MetricsType) {
        match metrics {
            MetricsType::RedisStreamLag(lag) => {
                crate::observability::logger::Logger::sys_debug(
                    "worker.metrics",
                    &format!("Worker Metrics: Current consumer group stream lag recorded: {}", lag),
                );
            }
            MetricsType::HandlerLatencyMs(latency) => {
                crate::observability::logger::Logger::sys_debug(
                    "worker.metrics",
                    &format!("Worker Metrics: Dynamic execution latency recorded: {:.2} ms", latency),
                );
            }
            MetricsType::ActiveConnectionsCount(count) => {
                crate::observability::logger::Logger::sys_debug(
                    "worker.metrics",
                    &format!("Worker Metrics: Current active consumer connections: {}", count),
                );
            }
        }
    }
}
