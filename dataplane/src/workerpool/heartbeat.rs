use std::collections::HashSet;
use std::sync::{Arc, RwLock};
use tokio::time::{sleep, Duration};
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: workerpool/heartbeat.rs - BỘ GIA HẠN KHÓA PHÂN PHỐI TỰ ĐỘNG
/// ============================================================================
///
/// 📌 VAI TRÒ & NHIỆM VỤ:
///   - Quản lý danh sách các khóa Lease Lock đang chạy trên instance hiện tại (Registry).
///   - Chạy một vòng lặp ngầm (background loop) định kỳ quét và gia hạn TTL cho tất cả lock.
///   - Đảm bảo các job dài hạn (như tạo VM 3-5 phút) không bị mất lock giữa chừng.
///   - Thiết kế an toàn HA: Nếu node chết, loop dừng -> lock tự động hết hạn trên Redis sau 30s.
///

/// Registry luồng an toàn (Thread-Safe) lưu giữ các lock key đang hoạt động
pub struct ActiveLockRegistry {
    // Sử dụng RwLock của thư viện chuẩn để bảo đảm an toàn đa luồng (Multi-threading safety)
    // Phép đọc (get_all_keys) song song không bị block bởi nhau, ghi (register/deregister) độc quyền.
    locks: RwLock<HashSet<String>>,
}

impl ActiveLockRegistry {
    /// Khởi tạo Registry rỗng
    pub fn new() -> Self {
        Self {
            locks: RwLock::new(HashSet::new()),
        }
    }

    /// Đăng ký một Lock Key mới vào Registry khi Job bắt đầu chạy
    pub fn register(&self, lock_key: String) {
        if let Ok(mut w) = self.locks.write() {
            w.insert(lock_key);
        } else {
            Logger::sys_error(
                "lock.registry",
                "Lock registry RwLock was poisoned during register",
                "RWLOCK_POISONED",
            );
        }
    }

    /// Xóa Lock Key khỏi Registry khi Job kết thúc (hoặc panic/timeout)
    pub fn deregister(&self, lock_key: &str) {
        if let Ok(mut w) = self.locks.write() {
            w.remove(lock_key);
        } else {
            Logger::sys_error(
                "lock.registry",
                "Lock registry RwLock was poisoned during deregister",
                "RWLOCK_POISONED",
            );
        }
    }

    /// Lấy danh sách toàn bộ Lock Key đang hoạt động để gửi đi gia hạn hàng loạt (batching)
    pub fn get_all_keys(&self) -> Vec<String> {
        if let Ok(r) = self.locks.read() {
            r.iter().cloned().collect()
        } else {
            Vec::new()
        }
    }
}

/// Khởi chạy vòng lặp ngầm gia hạn khóa định kỳ
pub async fn start_heartbeat_loop(
    registry: Arc<ActiveLockRegistry>,
    redis_client: Arc<RedisClientManager>,
    ttl_secs: u64,
    interval_duration: Duration,
) {
    Logger::sys_info(
        "lock.heartbeat",
        "Lock Heartbeat Auto-Renewal background loop has been started"
    );

    loop {
        // Sleep trước khi thực hiện lần quét tiếp theo
        sleep(interval_duration).await;

        let keys = registry.get_all_keys();
        if keys.is_empty() {
            // Tiết kiệm tài nguyên: Không có lock nào thì không gọi Redis tránh lãng phí network
            continue;
        }

        // Thực hiện gia hạn hàng loạt bằng Redis Pipeline (Tối ưu I/O)
        match crate::infra::redis::query::bulk_expire_locks(redis_client.client(), &keys, ttl_secs).await {
            Ok(_) => {
                // Ghi nhận debug log khi gia hạn thành công (chỉ log ở mức system info)
                Logger::sys_info(
                    "lock.heartbeat",
                    &format!("Successfully renewed {} active lease locks via Redis pipeline", keys.len())
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "lock.heartbeat",
                    &format!("CRITICAL: Failed to bulk renew locks: {}", e),
                    "HEARTBEAT_BULK_EXPIRE_FAILED",
                );
            }
        }
    }
}
