use crate::policyengine::types::PolicySet;
use arc_swap::ArcSwap;
use std::sync::Arc;
use std::sync::Mutex;
use std::time::Instant;

/// ============================================================================
/// 📂 MODULE: policyengine/engine.rs - Bộ Quản Lý Snapshot In-Memory Lock-Free
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Duy trì cấu hình động (active snapshot) in-memory an toàn, hiệu năng tối đa.
///   - Triển khai thao tác thay thế nguyên tử (atomic swap) lock-free qua `ArcSwap`.
///   - Bảo vệ hệ thống khỏi reload storm thông qua cổng chờ Cooldown Gate.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - `active_snapshot` là nguồn sự thật duy nhất cho mọi hoạt động runtime của Dataplane.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Luồng đọc (Worker threads) chỉ có quyền đọc bất biến lock-free qua `current()`.
///   - Luồng ghi (Swap worker) được cô lập bằng `write_gate` Mutex, ngăn chặn tranh chấp ghi.
///
/// 🔄 CALLSITE FLOW:
///   - `current()` được gọi liên tục bởi các worker để nạp giới hạn xử lý.
///   - `swap()` được kích hoạt khi Dedicated Watcher Worker phát hiện thay đổi chính sách hợp lệ.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Tốc độ đọc cấu hình gần như tức thì (tương đương tốc độ đọc con trỏ thô), loại bỏ 100%
///     nguy cơ nghẽn I/O hay lock contention trên luồng hot-path.
///
pub struct PolicyEngine {
    /// Snapshot chính sách đang trực tiếp điều hành hệ thống (Lock-Free Reader).
    active_snapshot: ArcSwap<PolicySet>,

    /// Phiên bản cấu hình tốt gần nhất để khôi phục khi có sự cố.
    last_known_good: ArcSwap<PolicySet>,

    /// Cổng kiểm soát ghi ngăn ngừa xung đột ghi và reload storm.
    write_gate: Mutex<SwapGateMetadata>,
}

struct SwapGateMetadata {
    last_reload_at: Option<Instant>,
    last_checksum: String,
}

impl PolicyEngine {
    /// Khởi tạo bộ máy quản lý chính sách lock-free.
    pub fn new(initial: PolicySet) -> Self {
        let initial_arc = Arc::new(initial);
        Self {
            active_snapshot: ArcSwap::new(initial_arc.clone()),
            last_known_good: ArcSwap::new(initial_arc.clone()),
            write_gate: Mutex::new(SwapGateMetadata {
                last_reload_at: None,
                last_checksum: initial_arc.checksum_sha.clone(),
            }),
        }
    }

    /// Lấy con trỏ bất biến dẫn tới snapshot chính sách hiện tại.
    /// Phương thức này hoàn toàn lock-free và cực kỳ an toàn cho môi trường siêu song song.
    pub fn current(&self) -> Arc<PolicySet> {
        self.active_snapshot.load_full()
    }

    /// Thực hiện thay thế nguyên tử (atomic swap) chính sách mới vào RAM.
    ///
    /// # Các bộ lọc bảo vệ hệ thống (Protective Invariant Gates):
    ///   1. **Semantic Filter**: Validate YAML contract (version="v1", policies non-empty).
    ///   2. **Deduplication Filter**: Skip swap nếu checksum trùng khớp `last_checksum`.
    ///   3. **Cooldown Filter (5 giây)**: Skip swap nếu thời gian giãn cách quá ngắn chống reload storm.
    ///   4. **LKG Backup**: Lưu trữ snapshot cũ vào `last_known_good`.
    ///   5. **Atomic Swap**: Gọi `active_snapshot.store` hoán đổi con trỏ nguyên tử.
    pub fn swap(&self, new_policy: PolicySet) -> Result<(), String> {
        // Phase 1: Kiểm định cú pháp tệp trước khi khóa write gate
        new_policy.validate()?;

        // Phase 2: Đăng ký khóa ghi bảo vệ
        let mut gate = self.write_gate.lock().expect("CRITICAL: PolicyEngine write gate Mutex poisoned");

        // Step 2.1: Deduplication Check
        if gate.last_checksum == new_policy.checksum_sha {
            println!("Policy Engine: Swap skipped. Checksum is identical to active snapshot.");
            return Ok(());
        }

        // Step 2.2: Cooldown Check (5 seconds)
        let now = Instant::now();
        if let Some(last_reload) = gate.last_reload_at {
            if now.duration_since(last_reload).as_secs() < 5 {
                println!("Policy Engine: Swap skipped. Cooldown filter active (less than 5s since last reload).");
                return Ok(());
            }
        }

        // Step 2.3: Thực thi hoán đổi nguyên tử
        let new_arc = Arc::new(new_policy.clone());
        let old_active = self.active_snapshot.load_full();

        self.last_known_good.store(old_active);
        self.active_snapshot.store(new_arc);

        // Cập nhật siêu dữ liệu cổng ghi
        gate.last_checksum = new_policy.checksum_sha.clone();
        gate.last_reload_at = Some(now);

        println!("Policy Engine: Lock-free atomic swap completed successfully. Checksum: {}", new_policy.checksum_sha);
        Ok(())
    }
}
