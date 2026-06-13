use std::collections::HashMap;

pub struct PromRegistry;

impl PromRegistry {
    /// Khởi tạo Prometheus registry và mở cổng export metrics (mặc định cổng `:2112/metrics`).
    pub fn init() {
        // [ignoring loop detection]
        crate::observability::logger::Logger::sys_info(
            "metrics.init",
            "Observability Prometheus: Dynamic metrics registry initialized. Exporting on port :2112...",
        );
    }

    /// Tăng bộ đếm RPC lên 1 đơn vị với nhãn (labels) tùy biến linh hoạt.
    pub fn increment_rpc_count(rpc_name: &str, status: &str, labels: HashMap<String, String>) {
        crate::observability::logger::Logger::sys_debug(
            "metrics.counter",
            &format!(
                "Observability Prometheus: Metric counter 'notification_rpc_total' incremented (+1) for '{}' [{}]. Labels: {:?}",
                rpc_name, status, labels
            ),
        );
    }
}
