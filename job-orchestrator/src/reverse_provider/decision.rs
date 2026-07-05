/// [COMMENT]: Bộ ra quyết định Backpressure và Trạng thái Zone (Decision Engine).
/// Đặt ở tầng reverse_provider để điều phối cross-cutting giữa các provider:
/// zone, hypervisor, storage, mail — không thuộc sở hữu riêng của zone provider.
pub struct DecisionEngine;

impl DecisionEngine {
    /// Đánh giá trạng thái tối ưu của Zone và Service dựa trên tài nguyên thô và hàng đợi.
    /// Trả về: (zone_status mong muốn, mail_enabled mong muốn, storage_enabled mong muốn)
    pub fn evaluate(
        queue_len: i64,
        pending_len: i64,
        avg_cpu: f64,
        avg_ram: f64,
        mail_status: &str,
        mail_capacity: usize,
        storage_status: &str,
        storage_capacity: usize,
        current_zone_status: &str,
        current_mail_enabled: bool,
        current_storage_enabled: bool,
    ) -> (String, bool, bool) {
        let mut target_zone_status = current_zone_status.to_string();
        let mut target_mail_enabled = current_mail_enabled;
        let mut target_storage_enabled = current_storage_enabled;

        // [COMMENT]: 1. Logic quyết định cho Service Mail dựa trên sức khỏe vật lý của Stalwart.
        // down -> tắt service; healthy/degraded -> bật lại service.
        if mail_status == "down" {
            target_mail_enabled = false;
        } else if mail_status == "healthy" || mail_status == "degraded" {
            target_mail_enabled = true;
        }

        // [COMMENT]: 1.2. Logic quyết định cho Service Storage dựa trên sức khỏe vật lý của MinIO.
        // Tương tự mail, chỉ tắt khi down, bật lại khi healthy/degraded.
        if storage_status == "down" {
            target_storage_enabled = false;
        } else if storage_status == "healthy" || storage_status == "degraded" {
            target_storage_enabled = true;
        }

        // [COMMENT]: 2. Logic quyết định trạng thái tổng thể của Zone (State Machine Transition).
        // Chỉ tự động đánh giá và phục hồi đối với các trạng thái active, congested, draining.
        // Các trạng thái cấu hình thủ công của SRE (planned, maintenance, disabled) được bảo toàn tuyệt đối.
        match current_zone_status {
            "active" | "congested" | "draining" => {
                // [COMMENT]: Nếu dịch vụ mail hoặc storage sập hoàn toàn (hoặc degraded quá nặng),
                // lập tức kích hoạt draining để ngăn nhận job mới, bảo vệ tính nhất quán dữ liệu.
                if mail_status == "down"
                    || mail_capacity < 10
                    || storage_status == "down"
                    || storage_capacity < 10
                {
                    target_zone_status = "draining".to_string();
                } else {
                    // [COMMENT]: Ngưỡng quá tải (Congested Thresholds) - Hysteresis để tránh flapping trạng thái
                    let is_overloaded =
                        queue_len > 5000 || pending_len > 500 || avg_cpu > 0.90 || avg_ram > 0.90;

                    // [COMMENT]: Ngưỡng hồi phục (Recovery Thresholds - Hysteresis gap tránh oscillation)
                    let is_recovered = queue_len < 4000
                        && pending_len < 400
                        && avg_cpu < 0.85
                        && avg_ram < 0.85;

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
                            // [COMMENT]: Chỉ tự động phục hồi từ draining lên active khi cả
                            // mail và storage đều healthy và tải hệ thống đã hồi phục đủ.
                            if mail_status == "healthy"
                                && mail_capacity >= 50
                                && storage_status == "healthy"
                                && storage_capacity >= 50
                                && is_recovered
                            {
                                target_zone_status = "active".to_string();
                            }
                        }
                        _ => {}
                    }
                }
            }
            _ => {}
        }

        (target_zone_status, target_mail_enabled, target_storage_enabled)
    }
}
