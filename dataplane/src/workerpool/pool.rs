use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use tokio::sync::Notify;
use tokio_util::sync::CancellationToken;

use crate::workerpool::runtime::WorkerJobRuntime;

/// Owns execution-aware Tokio worker slots and the graceful-shutdown barrier.
///
/// One slot awaits exactly one `JobRunner` at a time. Scale-down transitions a
/// slot to `Draining`, stops further receives, and lets the current execution
/// reach its fenced durability boundary before the slot ID can be reused.
pub struct WorkerLifecycleManager {
    /// Token gốc dùng để phát tín hiệu dừng đồng loạt cho toàn bộ các worker đang chạy ngầm.
    cancel_token: CancellationToken,

    /// Registry quản lý slot và trạng thái draining của từng worker.
    active_workers: Mutex<HashMap<usize, WorkerSlot>>,

    /// Incarnation fence ngăn task cũ xoá slot mới sau khi worker ID được tái sử dụng.
    next_generation: AtomicU64,

    /// Bộ theo dõi và đếm số lượng tác vụ đang hoạt động phục vụ cho việc graceful shutdown.
    tracker: Arc<TaskTracker>,

    /// [COMMENT]: Một JMAP mail runtime dùng chung toàn pod để micro-batch job từ mọi worker.
    pub mail_runtime: Arc<crate::executor::mail::MailRuntime>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum WorkerState {
    Starting,
    Ready,
    Draining,
}

struct WorkerSlot {
    token: CancellationToken,
    generation: u64,
    state: WorkerState,
}

#[derive(Clone, Copy, Debug, Default)]
pub struct WorkerStateCounts {
    pub starting: usize,
    pub ready: usize,
    pub draining: usize,
}

impl WorkerStateCounts {
    pub fn total(self) -> usize {
        self.starting
            .saturating_add(self.ready)
            .saturating_add(self.draining)
    }
}

impl WorkerLifecycleManager {
    pub fn new(mail_runtime: Arc<crate::executor::mail::MailRuntime>) -> Self {
        Self {
            cancel_token: CancellationToken::new(),
            active_workers: Mutex::new(HashMap::new()),
            next_generation: AtomicU64::new(1),
            tracker: Arc::new(TaskTracker::new()),
            mail_runtime,
        }
    }

    /// Lấy bản sao của global cancel token
    pub fn cancel_token(&self) -> CancellationToken {
        self.cancel_token.clone()
    }

    /// Register a background task in the process-wide graceful-shutdown barrier.
    pub fn track_task(&self) -> TaskGuard {
        self.tracker.track()
    }

    pub(crate) fn task_tracker(&self) -> Arc<TaskTracker> {
        self.tracker.clone()
    }

    /// Cấp phát và khởi chạy một Worker (Luồng Worker xử lý Job từ channel) song song thực sự.
    pub fn spawn_worker(self: &Arc<Self>, worker_id: usize, runtime: Arc<WorkerJobRuntime>) {
        let child_token = self.cancel_token.child_token();
        let generation = self.next_generation.fetch_add(1, Ordering::Relaxed);

        // A draining/starting slot remains reserved until its task exits; this
        // prevents scale-up from reusing an ID while the old task is still alive.
        {
            let mut active = self.active_workers.lock().unwrap();
            if active.contains_key(&worker_id) {
                crate::observability::logger::Logger::sys_warn(
                    "worker.lifecycle",
                    &format!("Worker slot {worker_id} is already active; spawn ignored"),
                    "WORKER_SLOT_ALREADY_ACTIVE",
                );
                return;
            }
            active.insert(
                worker_id,
                WorkerSlot {
                    token: child_token.clone(),
                    generation,
                    state: WorkerState::Starting,
                },
            );
        }

        let worker_token = child_token.clone();
        let self_clone = self.clone();
        let guard = self.tracker.track();

        tokio::spawn(async move {
            let _guard = guard; // Giữ guard cho luồng worker
            let _registration = WorkerRegistrationGuard {
                manager: self_clone.clone(),
                worker_id,
                generation,
            };
            self_clone.mark_worker_ready(worker_id, generation);
            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Worker {} provisioned and starting active job processing loop...",
                    worker_id
                ),
            );

            loop {
                // Nhận job từ channel an toàn khi app shutdown hoặc worker bị dừng (Scale Down)
                let job_opt = tokio::select! {
                    biased;
                    _ = worker_token.cancelled() => {
                        break;
                    }
                    res = runtime.receive_job() => res
                };

                match job_opt {
                    Some(payload) => {
                        // The worker owns the execution await. This makes the
                        // target worker count an actual concurrency bound instead
                        // of a count of detached receiver tasks.
                        crate::job_lifecycle::runner::JobRunner::run_job(
                            payload,
                            self_clone.clone(),
                            runtime.job_runner_context().clone(),
                        )
                        .await;
                    }
                    None => {
                        break; // Channel closed
                    }
                }
            }

            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Worker {} has gracefully terminated.",
                    worker_id
                ),
            );
        });
    }

    /// Thu hồi và dừng an toàn (Scale Down) một Worker cụ thể.
    pub fn terminate_worker(&self, worker_id: usize) {
        let mut active = self.active_workers.lock().unwrap();
        if let Some(slot) = active.get_mut(&worker_id) {
            if slot.state == WorkerState::Draining {
                return;
            }
            slot.state = WorkerState::Draining;
            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Signaling Worker {} to gracefully shutdown...",
                    worker_id
                ),
            );
            slot.token.cancel();
        }
    }

    /// Lấy danh sách ID của các Worker hiện đang hoạt động.
    pub fn active_worker_ids(&self) -> Vec<usize> {
        let active = self.active_workers.lock().unwrap();
        active.keys().cloned().collect()
    }

    pub fn worker_state_counts(&self) -> WorkerStateCounts {
        let active = self.active_workers.lock().unwrap();
        let mut counts = WorkerStateCounts::default();
        for slot in active.values() {
            match slot.state {
                WorkerState::Starting => counts.starting = counts.starting.saturating_add(1),
                WorkerState::Ready => counts.ready = counts.ready.saturating_add(1),
                WorkerState::Draining => counts.draining = counts.draining.saturating_add(1),
            }
        }
        counts
    }

    fn mark_worker_ready(&self, worker_id: usize, generation: u64) {
        if let Ok(mut active) = self.active_workers.lock() {
            if let Some(slot) = active.get_mut(&worker_id) {
                if slot.generation == generation && slot.state == WorkerState::Starting {
                    slot.state = WorkerState::Ready;
                }
            }
        }
    }

    fn finish_worker(&self, worker_id: usize, generation: u64) {
        if let Ok(mut active) = self.active_workers.lock() {
            if active
                .get(&worker_id)
                .is_some_and(|slot| slot.generation == generation)
            {
                active.remove(&worker_id);
            }
        }
    }

    /// Phát tín hiệu dừng đồng loạt cho toàn bộ các worker và đợi toàn bộ hoàn thành (Graceful Shutdown).
    pub async fn shutdown(&self) {
        self.cancel_token.cancel();
        crate::observability::logger::Logger::sys_info(
            "worker.lifecycle",
            "Worker Pool: Global cancellation token triggered. Waiting for all workers & tasks to gracefully complete...",
        );
        // Every worker and its asynchronous cleanup/report child registers
        // before its parent guard exits, so the barrier cannot observe a false
        // zero between parent completion and child startup.
        self.tracker.wait().await;
        self.mail_runtime.shutdown().await;
        crate::observability::logger::Logger::sys_info(
            "worker.lifecycle",
            "Worker Pool: All workers and execution tasks have gracefully terminated.",
        );
    }
}

struct WorkerRegistrationGuard {
    manager: Arc<WorkerLifecycleManager>,
    worker_id: usize,
    generation: u64,
}

impl Drop for WorkerRegistrationGuard {
    fn drop(&mut self) {
        self.manager.finish_worker(self.worker_id, self.generation);
    }
}

/// ============================================================================
/// 📂 UTILS: TaskTracker - Bộ Đếm Theo Dõi Tác Vụ Để Graceful Shutdown
/// ============================================================================
pub struct TaskTracker {
    counter: Arc<AtomicUsize>,
    notify: Arc<Notify>,
}

impl TaskTracker {
    pub fn new() -> Self {
        Self {
            counter: Arc::new(AtomicUsize::new(0)),
            notify: Arc::new(Notify::new()),
        }
    }

    /// Đăng ký và bắt đầu theo dõi một tác vụ mới.
    pub fn track(&self) -> TaskGuard {
        self.counter.fetch_add(1, Ordering::SeqCst);
        TaskGuard {
            counter: self.counter.clone(),
            notify: self.notify.clone(),
        }
    }

    /// Đợi bất đồng bộ cho tới khi toàn bộ các tác vụ đang được theo dõi kết thúc (counter về 0).
    pub async fn wait(&self) {
        loop {
            // [COMMENT]: Đăng ký waiter trước khi đọc counter để không mất notify ở khe race
            // giữa lần đọc cuối cùng và lúc bắt đầu await.
            let notified = self.notify.notified();
            if self.counter.load(Ordering::SeqCst) == 0 {
                return;
            }
            notified.await;
        }
    }
}

/// Guard tự động giải phóng và giảm bộ đếm khi bị Drop (khi Task kết thúc).
pub struct TaskGuard {
    counter: Arc<AtomicUsize>,
    notify: Arc<Notify>,
}

impl Drop for TaskGuard {
    fn drop(&mut self) {
        if self.counter.fetch_sub(1, Ordering::SeqCst) == 1 {
            self.notify.notify_waiters();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{TaskTracker, WorkerStateCounts};
    use std::sync::Arc;
    use std::time::Duration;

    #[tokio::test]
    async fn shutdown_tracker_waits_for_child_after_parent_finishes() {
        let tracker = Arc::new(TaskTracker::new());
        let parent = tracker.track();
        let child = tracker.track();
        drop(parent);

        assert!(
            tokio::time::timeout(Duration::from_millis(10), tracker.wait())
                .await
                .is_err()
        );
        drop(child);
        tokio::time::timeout(Duration::from_millis(100), tracker.wait())
            .await
            .expect("tracker should complete after the last child exits");
    }

    #[test]
    fn worker_state_total_includes_draining_slots() {
        assert_eq!(
            WorkerStateCounts {
                starting: 1,
                ready: 2,
                draining: 3,
            }
            .total(),
            6
        );
    }
}
