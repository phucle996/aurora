use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use tokio::sync::mpsc;
use tokio::sync::Notify;
use tokio_util::sync::CancellationToken;

/// ============================================================================
/// 📂 MODULE: workerpool/lifecycle.rs - Trình Quản Lý Vòng Đời Worker Vật Lý
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Quản trị việc cấp phát (provisioning) các worker thô chạy dưới dạng tokio green tasks.
///   - Theo dõi sức khỏe hoạt động của các worker, thực hiện graceful shutdown và dynamic scale.
///   - Điều phối tắt an toàn toàn bộ worker pool (Graceful Termination) khi nhận tín hiệu kết thúc.
///
/// 🎯 NGUYÊN TẮC THIẾT KẾ & RANH GIỚI HỆ THỐNG (ARCHITECTURAL BOUNDARIES & CONTRACT):
///   1. KHÔNG DÙNG THÀNH PHẦN POOL NGOÀI (NO EXTERNAL POOL):
///      - Để tối ưu hóa hiệu năng, giảm thiểu việc khóa lock và tối giản token sử dụng, hệ thống
///        KHÔNG duy trì hàng đợi (task queue) hay hồ chứa luồng vật lý thủ công.
///      - Thay vào đó, nó tận dụng trực tiếp bộ lập lịch của Tokio (Work-stealing Runtime) vốn đã
///        là một thread pool hoàn chỉnh và hiệu năng cực cao.
///   2. ĐỊNH NGHĨA "WORKER" LÀ TOKIO GREEN TASK:
///      - Bất cứ nơi nào cần xử lý công việc bất đồng bộ (ví dụ: ingestion loop hoặc các workloads như mail),
///        caller chỉ cần gọi `WorkerLifecycleManager::spawn(...)`.
///      - Việc này sẽ cấp phát động một worker (tokio green task) dưới sự giám sát vòng đời của Lifecycle Manager.
///   3. QUẢN LÝ VÒNG ĐỜI VÀ CỦA LỚP PHÒNG THỦ CẮT GIẢM (GRACEFUL TERMINATION):
///      - Mọi tác vụ được spawn qua Lifecycle Manager đều bắt buộc liên kết với `CancellationToken`.
///      - Khi hệ thống nhận tín hiệu shutdown, toàn bộ các tác vụ đang thực thi sẽ được đợi để hoàn thành.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Vòng đời và trạng thái hoạt động thực của các luồng tokio (`JoinHandle`).
///   - Trạng thái cấu hình số lượng worker hiện hành được cung cấp bởi `Config` thông qua biến môi trường.
///
pub struct WorkerLifecycleManager {
    /// Token gốc dùng để phát tín hiệu dừng đồng loạt cho toàn bộ các worker đang chạy ngầm.
    cancel_token: CancellationToken,

    /// Kênh truyền nhận tín hiệu điều khiển trạng thái sống của các worker.
    // Thêm #[allow(dead_code)] để tránh cảnh báo biên dịch do loại bỏ watcher động.
    #[allow(dead_code)]
    signal_sender: mpsc::Sender<WorkerSignal>,

    /// Registry quản lý CancellationToken của từng Worker đang hoạt động song song.
    active_workers: Mutex<HashMap<usize, CancellationToken>>,

    /// Bộ theo dõi và đếm số lượng tác vụ đang hoạt động phục vụ cho việc graceful shutdown.
    tracker: Arc<TaskTracker>,

    /// [COMMENT]: Một JMAP mail runtime dùng chung toàn pod để micro-batch job từ mọi worker.
    pub mail_runtime: Arc<crate::executor::mail::MailRuntime>,
}

/// Các loại tín hiệu điều phối vòng đời của worker
// Thêm #[allow(dead_code)] để duy trì thiết kế mở rộng phục vụ việc tự động hồi sinh worker khi panic trong tương lai.
#[allow(dead_code)]
pub enum WorkerSignal {
    /// Báo động khẩn cấp yêu cầu hồi sinh một worker cụ thể theo ID do bị crash/panic
    RestartWorker(usize),
}

impl WorkerLifecycleManager {
    pub fn new(
        mail_runtime: Arc<crate::executor::mail::MailRuntime>,
    ) -> (Self, mpsc::Receiver<WorkerSignal>) {
        let (tx, rx) = mpsc::channel(100);
        let manager = Self {
            cancel_token: CancellationToken::new(),
            signal_sender: tx,
            active_workers: Mutex::new(HashMap::new()),
            tracker: Arc::new(TaskTracker::new()),
            mail_runtime,
        };
        (manager, rx)
    }

    /// Lấy bản sao của global cancel token
    pub fn cancel_token(&self) -> CancellationToken {
        self.cancel_token.clone()
    }

    /// [COMMENT]: JobRunner chạy detached khỏi worker receive-loop nhưng vẫn phải nằm trong cùng
    /// shutdown barrier; nếu không pod có thể đóng MailRuntime khi job đang chuẩn bị enqueue mail.
    pub fn track_task(&self) -> TaskGuard {
        self.tracker.track()
    }

    /// Cấp phát và khởi chạy một Worker (Luồng Worker xử lý Job từ channel) song song thực sự.
    pub async fn spawn_worker(
        self: &Arc<Self>,
        worker_id: usize,
        config: Arc<crate::config::Config>,
        redis_job: Arc<crate::infra::redis::RedisClientManager>,
        redis_internal_zone: Arc<crate::infra::redis::RedisClientManager>,
        active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
        rx: Arc<
            tokio::sync::Mutex<
                tokio::sync::mpsc::Receiver<crate::job_lifecycle::message::JobPayload>,
            >,
        >,
        active_jobs: Arc<std::sync::atomic::AtomicUsize>,
    ) {
        let child_token = self.cancel_token.child_token();

        // Đăng ký token vào danh sách đang hoạt động
        {
            let mut active = self.active_workers.lock().unwrap();
            active.insert(worker_id, child_token.clone());
        }

        let worker_token = child_token.clone();
        let self_clone = self.clone();
        let guard = self.tracker.track();

        let rx = rx.clone();
        let active_jobs = active_jobs.clone();
        let stream_key = format!("jobs:{}", config.zone_id);

        tokio::spawn(async move {
            let _guard = guard; // Giữ guard cho luồng worker
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
                    _ = worker_token.cancelled() => {
                        break;
                    }
                    res = async {
                        let mut rx_guard = rx.lock().await;
                        rx_guard.recv().await
                    } => res
                };

                match job_opt {
                    Some(payload) => {
                        crate::job_lifecycle::runner::JobRunner::run_job(
                            payload,
                            self_clone.clone(),
                            redis_job.clone(),
                            redis_internal_zone.clone(),
                            active_lock_registry.clone(),
                            active_jobs.clone(),
                            stream_key.clone(),
                        );
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
        if let Some(token) = active.remove(&worker_id) {
            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Signaling Worker {} to gracefully shutdown...",
                    worker_id
                ),
            );
            token.cancel();
        }
    }

    /// Lấy danh sách ID của các Worker hiện đang hoạt động.
    pub fn active_worker_ids(&self) -> Vec<usize> {
        let active = self.active_workers.lock().unwrap();
        active.keys().cloned().collect()
    }

    // Đã loại bỏ hoàn toàn spawn_dedicated_policy_watcher ở đây do overengineering.

    /// Phát tín hiệu dừng đồng loạt cho toàn bộ các worker và đợi toàn bộ hoàn thành (Graceful Shutdown).
    pub async fn shutdown(&self) {
        self.cancel_token.cancel();
        crate::observability::logger::Logger::sys_info(
            "worker.lifecycle",
            "Worker Pool: Global cancellation token triggered. Waiting for all workers & tasks to gracefully complete...",
        );
        // [COMMENT]: Cancel intake trước, đợi mọi detached JobRunner submit/nhận kết quả xong rồi mới đóng batcher;
        // worker receive-loop cũng được track nên không thể phát sinh job mới sau khi barrier hoàn tất.
        self.tracker.wait().await;
        self.mail_runtime.shutdown().await;
        crate::observability::logger::Logger::sys_info(
            "worker.lifecycle",
            "Worker Pool: All workers and execution tasks have gracefully terminated.",
        );
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
