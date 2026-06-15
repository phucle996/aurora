use crate::observability::logger::Logger;
use crate::observability::resource::ResourceMonitor;

/// ============================================================================
/// 📂 MODULE: job_receiver/admission.rs - Bộ Điều Phối & Quản Lý Ngắt Mạch Tải (Admission Controller)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Độc lập quản lý và giám sát tài nguyên của hệ thống Dataplane.
///   - Tính toán nhịp kéo giãn (Pacing Delay) và trạng thái ngắt mạch (Circuit Breaker Hysteresis).
///   - Ngăn chặn triệt để tình trạng quá tải cục bộ khi xử lý Job.
///
pub struct AdmissionController {
    is_circuit_broken: bool,
    base_delay_ms: f64,
}

pub struct AdmissionResult {
    /// Cờ hiệu báo mạch có bị ngắt (OPEN) hay không.
    pub is_broken: bool,
    /// Độ trễ kéo giãn tính bằng mili giây.
    pub pacing_delay_ms: u64,
}

impl AdmissionController {
    /// Khởi tạo Admission Controller.
    pub fn new() -> Self {
        Self {
            is_circuit_broken: false,
            base_delay_ms: 1000.0,
        }
    }

    /// Đánh giá tải hiện tại của hệ thống và cập nhật trạng thái ngắt mạch.
    pub fn evaluate(&mut self, current_active: usize, max_workers: usize) -> AdmissionResult {
        let active_ratio = if max_workers > 0 {
            current_active as f64 / max_workers as f64
        } else {
            0.0
        };

        let cpu_usage = ResourceMonitor::cpu_usage();
        let ram_usage = ResourceMonitor::ram_usage();

        // R là giá trị lớn nhất trong ba chỉ số tài nguyên
        let r = active_ratio.max(cpu_usage).max(ram_usage);

        // 1. Tính toán Trọng số kéo tin (Pull Weight W) tuyến tính
        let w = if r < 0.8 {
            1.0 - (r / 0.8)
        } else {
            0.0
        };

        // 2. Tính toán nhịp trễ kéo giãn (Pacing Delay)
        let pacing_delay_ms = (self.base_delay_ms * (1.0 - w)) as u64;

        // 3. Kiểm thử điều kiện ngắt mạch (Circuit Breaker Hysteresis Check)
        if !self.is_circuit_broken && r >= 0.8 {
            self.is_circuit_broken = true;
            Logger::sys_warn(
                "job.admission_control",
                &format!(
                    "CRITICAL: Local instance resource load is too high ({:.1}% - active: {}/{}, CPU: {:.1}%, RAM: {:.1}%). Circuit Breaker OPEN: Pausing job ingestion loop...",
                    r * 100.0,
                    current_active,
                    max_workers,
                    cpu_usage * 100.0,
                    ram_usage * 100.0
                ),
                "High Load Circuit Breaker OPEN"
            );
        } else if self.is_circuit_broken && r <= 0.5 {
            self.is_circuit_broken = false;
            Logger::sys_info(
                "job.admission_control",
                &format!(
                    "RECOVERY: Resource load successfully recovered below safe threshold ({:.1}% - active: {}/{}, CPU: {:.1}%, RAM: {:.1}%). Circuit Breaker CLOSED: Resuming job ingestion loop...",
                    r * 100.0,
                    current_active,
                    max_workers,
                    cpu_usage * 100.0,
                    ram_usage * 100.0
                )
            );
        }

        AdmissionResult {
            is_broken: self.is_circuit_broken,
            pacing_delay_ms,
        }
    }
}
