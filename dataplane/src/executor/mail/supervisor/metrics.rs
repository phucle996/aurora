use std::sync::atomic::{AtomicU64, AtomicUsize};

/// [COMMENT]: Một bộ đếm dùng chung toàn pod; processor chỉ cập nhật, supervisor chỉ đọc để tính capacity.
#[derive(Default)]
pub struct MailWorkloadMetrics {
    pub pending_items: AtomicUsize,
    pub in_flight_batches: AtomicUsize,
    pub accepted_total: AtomicU64,
    pub failed_total: AtomicU64,
}
