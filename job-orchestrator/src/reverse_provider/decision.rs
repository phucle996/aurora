/// [COMMENT]: Bộ ra quyết định Backpressure và Trạng thái Zone (Decision Engine).
/// Đặt ở tầng reverse_provider để điều phối cross-cutting giữa các provider:
/// zone, hypervisor, storage, mail — không thuộc sở hữu riêng của zone provider.
///
/// NGUYÊN TẮC ENABLED-ONLY EVALUATION:
/// DecisionEngine chỉ nhận input từ service đang ENABLED trên zone đó.
/// Service bị tắt (desired_state = false) được coi là "không liên quan" —
/// không tham gia vào draining trigger hay recovery condition.
/// Việc bật/tắt service là quyết định của SRE qua UpdateZoneService, không phải Decision Engine.
pub struct DecisionEngine;

impl DecisionEngine {
    /// [COMMENT]: Đánh giá trạng thái tối ưu của Zone dựa trên tài nguyên thô và hàng đợi.
    /// Chỉ xét các service đang enabled — service disabled được bỏ qua hoàn toàn.
    ///
    /// Trả về: target_zone_status (String).
    /// Decision Engine KHÔNG tự động toggle desired_state của service.
    pub fn evaluate(
        queue_len: i64,
        pending_len: i64,
        avg_cpu: f64,
        avg_ram: f64,
        mail_status: &str,
        mail_capacity: usize,
        mail_enabled: bool,
        storage_status: &str,
        storage_capacity: usize,
        storage_enabled: bool,
        current_zone_status: &str,
    ) -> String {
        // [COMMENT]: 2. Logic quyết định trạng thái tổng thể của Zone (State Machine Transition).
        // Chỉ tự động đánh giá và phục hồi đối với các trạng thái active, congested, draining.
        // Các trạng thái cấu hình thủ công của SRE (planned, maintenance, disabled) được bảo toàn tuyệt đối.
        match current_zone_status {
            "active" | "congested" | "draining" => {
                // [COMMENT]: ENABLED-ONLY draining trigger.
                // Chỉ kéo zone về draining khi service ĐANG ĐƯỢC BẬT bị sập hoặc capacity quá thấp.
                // Service bị tắt (mail_enabled=false) hoàn toàn không ảnh hưởng quyết định này.
                let mail_failing = mail_enabled && (mail_status == "down" || mail_capacity < 10);
                let storage_failing =
                    storage_enabled && (storage_status == "down" || storage_capacity < 10);

                if mail_failing || storage_failing {
                    return "draining".to_string();
                }

                // [COMMENT]: Ngưỡng quá tải (Congested Thresholds) - Hysteresis để tránh flapping trạng thái
                let is_overloaded =
                    queue_len > 5000 || pending_len > 500 || avg_cpu > 0.90 || avg_ram > 0.90;

                // [COMMENT]: Ngưỡng hồi phục (Recovery Thresholds - Hysteresis gap tránh oscillation)
                let is_recovered =
                    queue_len < 4000 && pending_len < 400 && avg_cpu < 0.85 && avg_ram < 0.85;

                match current_zone_status {
                    "active" => {
                        if is_overloaded {
                            return "congested".to_string();
                        }
                    }
                    "congested" => {
                        if is_recovered {
                            return "active".to_string();
                        }
                    }
                    "draining" => {
                        // [COMMENT]: ENABLED-ONLY recovery condition.
                        // Zone thoát draining khi tất cả SERVICE ĐANG BẬT đều healthy.
                        // Service disabled được coi là "OK" theo phép toán: !enabled || (healthy && cap>=50).
                        // Ví dụ: zone chỉ có storage enabled → chỉ cần storage healthy là đủ.
                        let mail_ok =
                            !mail_enabled || (mail_status == "healthy" && mail_capacity >= 50);
                        let storage_ok = !storage_enabled
                            || (storage_status == "healthy" && storage_capacity >= 50);

                        if mail_ok && storage_ok && is_recovered {
                            return "active".to_string();
                        }
                    }
                    _ => {}
                }
            }
            _ => {}
        }

        // [COMMENT]: Không có thay đổi trạng thái — giữ nguyên current_zone_status
        current_zone_status.to_string()
    }
}
