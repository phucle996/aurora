pub(crate) struct MailBackpressureSnapshot {
    pub status: &'static str,
    pub capacity: usize,
}

impl MailBackpressureSnapshot {
    /// [COMMENT]: Policy chỉ diễn giải pressure hiện tại; bounded queue trong processor mới là nơi enforce giới hạn thật.
    pub(crate) fn calculate(
        disabled: bool,
        transport_healthy: bool,
        pending_items: usize,
        queue_capacity: usize,
    ) -> Self {
        if disabled || !transport_healthy {
            return Self {
                status: "down",
                capacity: 0,
            };
        }
        let queue_ratio = (pending_items as f64 / queue_capacity.max(1) as f64).clamp(0.0, 1.0);
        let capacity = ((1.0 - queue_ratio) * 100.0) as usize;
        Self {
            status: if capacity < 10 { "degraded" } else { "healthy" },
            capacity,
        }
    }
}
