/// ============================================================================
/// 📂 MODULE: workerpool/auto_scale.rs - Trình Điều Phối Co Giãn Số Lượng Worker
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Tính toán và đưa ra quyết định thay đổi số lượng worker hoạt động tại runtime.
///   - Đảm bảo giới hạn trên của worker không vượt quá định mức quy định bởi chính sách `max_workers` của Policy Engine.
///   - Tích hợp **Hard Resource Safeguard** nhằm đóng băng co giãn khi hệ thống đạt ngưỡng tài nguyên cao.
///
pub struct AutoScaleEngine {
    /// Giới hạn tối đa số lượng worker được phép cấp phát tại một thời điểm.
    max_workers: usize,
}

impl AutoScaleEngine {
    /// Khởi tạo bộ máy autoscaler với giới hạn trần cấu hình.
    pub fn new(max_workers: usize) -> Self {
        Self { max_workers }
    }

    /// Đánh giá tải thực tế dựa theo chỉ số đo đạc để đưa ra số lượng worker mục tiêu (Target Worker Scale).
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   1. **Hard Resource Safeguard**: Nếu số lượng kết nối/luồng đang mở đạt tới 90% giới hạn cứng `max_workers` của Policy Engine,
    ///      đóng băng ngay lập tức việc co giãn thêm để tránh sụt tài nguyên hệ thống (OOM/CPU thrashing).
    ///   2. Đánh giá tải theo **Queue Lag** và **Latency** để quyết định co giãn luồng xử lý.
    pub fn evaluate_scale(
        &self,
        current_workers: usize,
        lag: u64,
        latency_ms: f64,
        active_connections: usize,
    ) -> usize {
        // 1. Kiểm thử ngưỡng tài nguyên cứng (90% Safeguard)
        let hard_limit = (self.max_workers as f64 * 0.9) as usize;
        if active_connections >= hard_limit {
            crate::observability::logger::Logger::sys_warn(
                "worker.scaler",
                &format!(
                    "Autoscaler ALERT: Hard resource threshold reached ({}/{}). Freeze local scaling up!",
                    active_connections, self.max_workers
                ),
                "hard_resource_threshold",
            );
            return current_workers;
        }

        // 2. Co giãn luồng xử lý dựa trên Lag và Latency
        if lag > 100 || latency_ms > 500.0 {
            // Tải cao hoặc xử lý chậm -> Tăng thêm luồng xử lý (mỗi lần tăng 2 worker)
            let target = (current_workers + 2).min(self.max_workers);
            crate::observability::logger::Logger::sys_info(
                "worker.scaler",
                &format!(
                    "Autoscaler: High load detected (lag={}, latency={:.2}ms). Scaling up target: {} workers (cap={})",
                    lag, latency_ms, target, self.max_workers
                ),
            );
            target
        } else if lag == 0 {
            // Hàng đợi rỗng hoàn toàn -> Tiết kiệm tài nguyên scale về 0 luồng.
            // Không log tại đây: caller chịu trách nhiệm log khi target thực sự đổi.
            0
        } else {
            // Giữ nguyên quy mô hiện hành
            current_workers
        }
    }
}
