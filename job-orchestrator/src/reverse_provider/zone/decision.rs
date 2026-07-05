/// Bộ ra quyết định Backpressure và Trạng thái Zone (Decision Engine)
pub struct DecisionEngine;

impl DecisionEngine {
    /// Đánh giá trạng thái tối ưu của Zone và Service dựa trên tài nguyên thô và hàng đợi
    /// Trả về: (Trạng thái Zone mong muốn, Trạng thái Service mail enabled mong muốn)
    pub fn evaluate(
        queue_len: i64,
        pending_len: i64,
        avg_cpu: f64,
        avg_ram: f64,
        mail_status: &str,
        mail_capacity: usize,
        current_zone_status: &str,
        current_mail_enabled: bool,
    ) -> (String, bool) {
        let mut target_zone_status = current_zone_status.to_string();
        let mut target_mail_enabled = current_mail_enabled;

        // 1. Logic quyết định cho Service Mail dựa trên sức khỏe vật lý của Stalwart
        if mail_status == "down" {
            target_mail_enabled = false;
        } else if mail_status == "healthy" || mail_status == "degraded" {
            target_mail_enabled = true;
        }

        // 2. Logic quyết định trạng thái tổng thể của Zone (State Machine Transition)
        // [COMMENT]: Chỉ tự động đánh giá và phục hồi đối với các trạng thái active, congested, draining.
        // Các trạng thái cấu hình thủ công của SRE (planned, maintenance, disabled) được bảo toàn tuyệt đối.
        match current_zone_status {
            "active" | "congested" | "draining" => {
                if mail_status == "down" || mail_capacity < 10 {
                    target_zone_status = "draining".to_string();
                } else {
                    // Ngưỡng quá tải (Congested Thresholds)
                    let is_overloaded =
                        queue_len > 5000 || pending_len > 500 || avg_cpu > 0.90 || avg_ram > 0.90;

                    // Ngưỡng hồi phục (Recovery Thresholds - Hysteresis)
                    let is_recovered =
                        queue_len < 4000 && pending_len < 400 && avg_cpu < 0.85 && avg_ram < 0.85;

                    match current_zone_status {
                        "active" => {
                            if is_overloaded {
                                target_zone_status = "congested".to_string();
                            }
                        }
                        "congested" => {
                            if is_recovered {
                                target_zone_status = "active".to_string();
                            }
                        }
                        "draining" => {
                            // [COMMENT]: Chỉ tự động phục hồi từ draining lên active khi mail healthy và tải hệ thống giảm.
                            if mail_status == "healthy" && mail_capacity >= 50 && is_recovered {
                                target_zone_status = "active".to_string();
                            }
                        }
                        _ => {}
                    }
                }
            }
            _ => {}
        }

        (target_zone_status, target_mail_enabled)
    }
}
