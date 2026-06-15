use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use tokio::time::{sleep, Duration, Instant};
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
// Import từ module job_lifecycle mới đổi tên để báo cáo kết quả và quản lý cấu trúc báo cáo.
use crate::job_lifecycle::result::{JobExecutionResult, JobResultReporter};

/// ============================================================================
/// 📂 MODULE: workerpool/watchdog.rs - WATCHDOG GIÁM SÁT & CƯỠNG CHẾ KHÓA LEASE
/// ============================================================================
///
/// 📌 VAI TRÒ & NHIỆM VỤ:
///   - Quản lý danh sách và metadata của các khóa Lease Lock đang chạy (Registry).
///   - Chạy vòng lặp watchdog (background loop) mỗi 10s:
///     + Kiểm tra thời gian đã chạy của từng tác vụ.
///     + Nếu chưa vượt quá hạn mức (started_at + limit > now): Gia hạn lock trên Redis thêm 30s.
///     + Nếu đã vượt hạn mức (Timeout): Ngừng gia hạn lock, gọi abort_handle.abort() để giải phóng worker,
///       và tự động báo cáo trạng thái FAILED/EXECUTION_TIMEOUT lên Controlplane.
///

pub struct ActiveLockInfo {
    pub started_at: Instant,
    pub max_execution_limit: Duration,
    pub abort_handle: tokio::task::AbortHandle,
    pub job_id: String,
    pub job_version: u32,
    pub attempt: u32,
}

/// Registry luồng an toàn (Thread-Safe) lưu trữ các lock key đang hoạt động
pub struct ActiveLockRegistry {
    locks: RwLock<HashMap<String, ActiveLockInfo>>,
}

impl ActiveLockRegistry {
    /// Khởi tạo Registry rỗng
    pub fn new() -> Self {
        Self {
            locks: RwLock::new(HashMap::new()),
        }
    }

    /// Đăng ký một Lock Key mới cùng metadata và AbortHandle vào Registry
    pub fn register(
        &self,
        lock_key: String,
        started_at: Instant,
        max_execution_limit: Duration,
        abort_handle: tokio::task::AbortHandle,
        job_id: String,
        job_version: u32,
        attempt: u32,
    ) {
        if let Ok(mut w) = self.locks.write() {
            w.insert(
                lock_key,
                ActiveLockInfo {
                    started_at,
                    max_execution_limit,
                    abort_handle,
                    job_id,
                    job_version,
                    attempt,
                },
            );
        } else {
            Logger::sys_error(
                "lock.registry",
                "Lock registry RwLock was poisoned during register",
                "RWLOCK_POISONED",
            );
        }
    }

    /// Xóa Lock Key khỏi Registry khi Job kết thúc bình thường
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

    /// Lấy bản sao danh sách để kiểm tra và gia hạn hàng loạt
    pub fn get_all_active_locks(&self) -> Vec<(String, Instant, Duration, tokio::task::AbortHandle, String, u32, u32)> {
        if let Ok(r) = self.locks.read() {
            r.iter()
                .map(|(k, v)| {
                    (
                        k.clone(),
                        v.started_at,
                        v.max_execution_limit,
                        v.abort_handle.clone(),
                        v.job_id.clone(),
                        v.job_version,
                        v.attempt,
                    )
                })
                .collect()
        } else {
            Vec::new()
        }
    }
}

/// Khởi chạy vòng lặp ngầm Watchdog kiểm soát chất lượng thực thi và gia hạn khóa
pub async fn start_watchdog_loop(
    registry: Arc<ActiveLockRegistry>,
    redis_client: Arc<RedisClientManager>,
    ttl_secs: u64,
    interval_duration: Duration,
) {
    Logger::sys_info(
        "lock.watchdog",
        "Watchdog & Lock Lease Auto-Renewal background loop has been started"
    );

    loop {
        // Sleep trước khi thực hiện lần quét tiếp theo
        sleep(interval_duration).await;

        let active_locks = registry.get_all_active_locks();
        if active_locks.is_empty() {
            continue;
        }

        let mut keys_to_renew = Vec::new();
        let now = Instant::now();

        for (lock_key, started_at, max_limit, abort_handle, job_id, job_version, attempt) in active_locks {
            let elapsed = now.duration_since(started_at);
            if elapsed >= max_limit {
                // Tác vụ đã vượt quá hạn mức tối đa cho phép chạy (TIMEOUT)
                Logger::sys_warn(
                    "lock.watchdog",
                    &format!(
                        "CRITICAL: Job {} exceeded max execution limit ({:?}). Forcing cancellation...",
                        job_id, max_limit
                    ),
                    "JOB_EXECUTION_TIMEOUT_FORCED_KILL",
                );

                // 1. Thực hiện Hủy cưỡng chế tác vụ
                abort_handle.abort();

                // 2. Đồng bộ xóa ra khỏi Registry
                registry.deregister(&lock_key);

                // 3. Khởi chạy tác vụ gửi báo cáo lỗi timeout lên Redis Stream (durable) cho Job-Proxy
                let client_clone = redis_client.client().clone();
                tokio::spawn(async move {
                    let timeout_report = JobExecutionResult {
                        job_id,
                        job_version,
                        attempt,
                        result_status: "FAILED".to_string(),
                        error_code: Some("EXECUTION_TIMEOUT".to_string()),
                        message: "Job execution aborted by watchdog due to timeout".to_string(),
                    };
                    let _ = JobResultReporter::report_outcome(&client_clone, &timeout_report).await;
                });
            } else {
                // Tác vụ hoạt động bình thường, đưa vào danh sách gia hạn
                keys_to_renew.push(lock_key);
            }
        }

        // Thực hiện gia hạn hàng loạt các lock hợp lệ bằng Redis Pipeline
        if !keys_to_renew.is_empty() {
            match crate::infra::redis::query::bulk_expire_locks(redis_client.client(), &keys_to_renew, ttl_secs).await {
                Ok(_) => {
                    Logger::sys_info(
                        "lock.watchdog",
                        &format!("Successfully renewed {} active lease locks via Redis pipeline", keys_to_renew.len())
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "lock.watchdog",
                        &format!("CRITICAL: Failed to bulk renew locks: {}", e),
                        "WATCHDOG_BULK_EXPIRE_FAILED",
                    );
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::RedisTlsMode;

    #[tokio::test]
    async fn test_watchdog_timeout_abort() {
        let registry = Arc::new(ActiveLockRegistry::new());
        let redis_mgr = Arc::new(RedisClientManager::new(
            "redis://127.0.0.1:6379",
            RedisTlsMode::Disable,
            &None,
            &None,
            &None,
        ).unwrap());

        // Spawn a dummy long running task that sleeps for 10 seconds
        let task_handle = tokio::spawn(async {
            sleep(Duration::from_secs(10)).await;
            "finished"
        });

        // Register it with 1 second execution limit
        registry.register(
            "locks:job:test_abort".to_string(),
            Instant::now(),
            Duration::from_secs(1),
            task_handle.abort_handle(),
            "test_abort_id".to_string(),
            1,
            0,
        );

        // Start watchdog loop in background with a fast interval (100ms)
        let registry_clone = registry.clone();
        let watchdog_handle = tokio::spawn(async move {
            start_watchdog_loop(
                registry_clone,
                redis_mgr,
                30,
                Duration::from_millis(100),
            ).await;
        });

        // Wait for the task to be aborted
        let join_res = task_handle.await;
        assert!(join_res.is_err()); // Must be aborted
        assert!(join_res.unwrap_err().is_cancelled());

        // Check that it is deregistered
        assert!(registry.get_all_active_locks().is_empty());

        // Cleanup watchdog
        watchdog_handle.abort();
    }
}

