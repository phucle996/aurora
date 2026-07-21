use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::observability::logger::Logger;
use futures_util::{stream, StreamExt};
use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use tokio::time::{sleep, Duration, Instant};
// Import từ module job_lifecycle mới đổi tên để báo cáo kết quả và quản lý cấu trúc báo cáo.
use crate::job_lifecycle::result::{JobExecutionResult, JobResultReporter};

// ============================================================================
// 📂 MODULE: workerpool/watchdog.rs - WATCHDOG GIÁM SÁT & CƯỠNG CHẾ KHÓA LEASE
// ============================================================================
// Registry theo dõi deadline và renew fenced lease trên NATS KV mỗi 10 giây.
#[derive(Clone)]
pub struct ActiveLockInfo {
    pub started_at: Instant,
    pub max_execution_limit: Duration,
    pub abort_handle: tokio::task::AbortHandle,
    pub job_id: String,
    pub job_version: u32,
    pub attempt: u32,
    pub job_topic: String,
    pub source_domain: String,
    pub trace_id: String,
    pub lease: ZoneLease,
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
    #[allow(clippy::too_many_arguments)] // [COMMENT]: Giữ metadata đăng ký hiện rõ tại callsite, không giấu trong builder/helper.
    pub fn register(
        &self,
        lock_key: String,
        started_at: Instant,
        max_execution_limit: Duration,
        abort_handle: tokio::task::AbortHandle,
        job_id: String,
        job_version: u32,
        attempt: u32,
        job_topic: String,
        source_domain: String,
        trace_id: String,
        lease: ZoneLease,
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
                    job_topic,
                    source_domain,
                    trace_id,
                    lease,
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
    pub fn get_all_active_locks(&self) -> Vec<(String, ActiveLockInfo)> {
        if let Ok(r) = self.locks.read() {
            r.iter()
                .map(|(key, info)| (key.clone(), info.clone()))
                .collect()
        } else {
            Vec::new()
        }
    }
}

/// Khởi chạy vòng lặp ngầm Watchdog kiểm soát chất lượng thực thi và gia hạn khóa
pub async fn start_watchdog_loop(
    registry: Arc<ActiveLockRegistry>,
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
    ttl_secs: u64,
    interval_duration: Duration,
) {
    Logger::sys_info(
        "lock.watchdog",
        "Watchdog & Lock Lease Auto-Renewal background loop has been started",
    );

    loop {
        // Sleep trước khi thực hiện lần quét tiếp theo
        sleep(interval_duration).await;

        let active_locks = registry.get_all_active_locks();
        if active_locks.is_empty() {
            continue;
        }

        let now = Instant::now();
        let mut renewals = Vec::with_capacity(active_locks.len());

        for (lock_key, info) in active_locks {
            let elapsed = now.duration_since(info.started_at);
            if elapsed >= info.max_execution_limit {
                // Tác vụ đã vượt quá hạn mức tối đa cho phép chạy (TIMEOUT)
                Logger::sys_warn(
                    "lock.watchdog",
                    &format!(
                        "CRITICAL: Job {} exceeded max execution limit ({:?}). Forcing cancellation...",
                        info.job_id, info.max_execution_limit
                    ),
                    "JOB_EXECUTION_TIMEOUT_FORCED_KILL",
                );

                // 1. Thực hiện Hủy cưỡng chế tác vụ
                info.abort_handle.abort();

                // 2. Đồng bộ xóa ra khỏi Registry
                registry.deregister(&lock_key);

                // 3. Khởi chạy tác vụ gửi báo cáo lỗi timeout lên Redis Job Stream (durable) cho Job-Proxy
                let client_clone = redis_job.client().clone();
                tokio::spawn(async move {
                    let timeout_report = JobExecutionResult {
                        job_id: info.job_id,
                        job_version: info.job_version,
                        attempt: info.attempt,
                        result_status: "FAILED".to_string(),
                        error_code: Some("EXECUTION_TIMEOUT".to_string()),
                        message: "Job execution aborted by watchdog due to timeout".to_string(),
                        job_topic: info.job_topic,
                        source_domain: info.source_domain,
                        trace_id: info.trace_id,
                    };
                    let _ = JobResultReporter::report_outcome(&client_clone, &timeout_report).await;
                });
            } else {
                renewals.push((lock_key, info.abort_handle, info.job_id, info.lease));
            }
        }

        // [COMMENT]: NATS KV không có pipeline như Redis; bounded concurrency tránh cộng dồn một RTT cho từng active job.
        stream::iter(renewals)
            .for_each_concurrent(32, |(lock_key, abort_handle, job_id, lease)| {
                let zone_kv = zone_kv.clone();
                let registry = registry.clone();
                async move {
                    match zone_kv
                        .renew_lease(&lease, Duration::from_secs(ttl_secs))
                        .await
                    {
                        Ok(true) => {}
                        Ok(false) | Err(_) => {
                            abort_handle.abort();
                            registry.deregister(&lock_key);
                            Logger::sys_error(
                                "lock.watchdog",
                                &format!(
                                    "Lost fenced Zone KV lease for job {job_id}; task aborted"
                                ),
                                "ZONE_KV_LEASE_LOST",
                            );
                        }
                    }
                }
            })
            .await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn registry_preserves_fenced_lease_and_deregisters() {
        let registry = Arc::new(ActiveLockRegistry::new());
        let task_handle = tokio::spawn(async {
            sleep(Duration::from_secs(10)).await;
            "finished"
        });
        registry.register(
            "lease.job.test".to_string(),
            Instant::now(),
            Duration::from_secs(1),
            task_handle.abort_handle(),
            "test_abort_id".to_string(),
            1,
            0,
            "test_topic".to_string(),
            "TEST".to_string(),
            "test_trace_id".to_string(),
            ZoneLease {
                key: "lease.job.test".to_string(),
                owner_id: "pod-a".to_string(),
                fencing_token: 7,
            },
        );
        let active = registry.get_all_active_locks();
        assert_eq!(active.len(), 1);
        assert_eq!(active[0].1.lease.fencing_token, 7);
        registry.deregister("lease.job.test");
        assert!(registry.get_all_active_locks().is_empty());
        task_handle.abort();
    }
}
