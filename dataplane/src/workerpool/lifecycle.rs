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
///   - Trạng thái cấu hình số lượng worker hiện hành được cung cấp bởi `policyengine`.
///
pub struct WorkerLifecycleManager {
    /// Token gốc dùng để phát tín hiệu dừng đồng loạt cho toàn bộ các worker đang chạy ngầm.
    cancel_token: CancellationToken,

    /// Kênh truyền nhận tín hiệu điều khiển trạng thái sống của các worker.
    signal_sender: mpsc::Sender<WorkerSignal>,

    /// Registry quản lý CancellationToken của từng Worker đang hoạt động song song.
    active_workers: Mutex<HashMap<usize, CancellationToken>>,

    /// Bộ theo dõi và đếm số lượng tác vụ đang hoạt động phục vụ cho việc graceful shutdown.
    tracker: Arc<TaskTracker>,
}

/// Các loại tín hiệu điều phối vòng đời của worker
pub enum WorkerSignal {
    /// Báo động khẩn cấp yêu cầu hồi sinh một worker cụ thể theo ID do bị crash/panic
    RestartWorker(usize),
}

impl WorkerLifecycleManager {
    /// Khởi tạo bộ máy quản lý vòng đời và trả về kênh lắng nghe tín hiệu phản hồi từ worker.
    pub fn new() -> (Self, mpsc::Receiver<WorkerSignal>) {
        let (tx, rx) = mpsc::channel(100);
        let manager = Self {
            cancel_token: CancellationToken::new(),
            signal_sender: tx,
            active_workers: Mutex::new(HashMap::new()),
            tracker: Arc::new(TaskTracker::new()),
        };
        (manager, rx)
    }

    /// Giao (spawn) một tác vụ bất đồng bộ cho một worker thực hiện và chờ kết quả.
    /// Không cần thông qua một task pool ngoài, caller tự lấy worker (tokio task) để xử lý.
    /// Tác vụ được tự động theo dõi để phục vụ Graceful Shutdown.
    pub async fn spawn<F, Fut, T>(&self, f: F) -> Result<T, String>
    where
        F: FnOnce() -> Fut + Send + 'static,
        Fut: std::future::Future<Output = T> + Send + 'static,
        T: Send + 'static,
    {
        let (tx, rx) = tokio::sync::oneshot::channel();
        let cancel_token = self.cancel_token.clone();
        let guard = self.tracker.track();

        tokio::spawn(async move {
            let _guard = guard; // Giữ guard để đếm tác vụ hoạt động
            tokio::select! {
                _ = cancel_token.cancelled() => {
                    // Hệ thống đang dừng, không thực hiện tiếp
                }
                res = f() => {
                    let _ = tx.send(res);
                }
            }
        });

        rx.await
            .map_err(|e| format!("Worker failed or task cancelled during execution: {}", e))
    }

    /// Cấp phát và khởi chạy một Worker (Luồng Ingestion Loop) song song thực sự.
    pub async fn spawn_worker(
        self: &Arc<Self>,
        worker_id: usize,
        config: Arc<crate::config::Config>,
        policy_engine: Arc<crate::policyengine::engine::PolicyEngine>,
        redis_job: Arc<crate::infra::redis::RedisClientManager>,
        redis_internal_zone: Arc<crate::infra::redis::RedisClientManager>,
        active_lock_registry: Arc<crate::workerpool::heartbeat::ActiveLockRegistry>,
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

        tokio::spawn(async move {
            let _guard = guard; // Giữ guard cho luồng ingestion
            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Worker {} provisioned and starting active job ingestion loop...",
                    worker_id
                ),
            );

            crate::job_receiver::consumer::JobConsumer::start_ingestion(
                config,
                policy_engine,
                redis_job,
                redis_internal_zone,
                worker_id,
                worker_token,
                self_clone,
                active_lock_registry,
            )
            .await;

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

    /// Cấp phát và khởi chạy một Worker chuyên biệt chuyên trách theo dõi chính sách (Dedicated Watcher Worker).
    pub async fn spawn_dedicated_policy_watcher<F, Fut>(
        &self,
        watcher_id: usize,
        make_watcher_future: F,
    ) where
        F: FnOnce(CancellationToken) -> Fut + Send + 'static,
        Fut: std::future::Future<Output = Result<(), String>> + Send + 'static,
    {
        let token = self.cancel_token.child_token();
        let tx = self.signal_sender.clone();
        let token_clone = token.clone();
        let guard = self.tracker.track();

        tokio::spawn(async move {
            let _guard = guard; // Giữ guard cho dedicated watcher
            crate::observability::logger::Logger::sys_info(
                "worker.lifecycle",
                &format!(
                    "Worker Pool: Dedicated Policy Watcher Worker {} started under lifecycle surveillance...",
                    watcher_id
                ),
            );

            // Khởi chạy future thực thi việc watch file/subscribe
            let watcher_future = make_watcher_future(token_clone);

            tokio::select! {
                _ = token.cancelled() => {
                    crate::observability::logger::Logger::sys_info(
                        "worker.lifecycle",
                        &format!(
                            "Worker Pool: Dedicated Policy Watcher Worker {} shutdown gracefully.",
                            watcher_id
                        ),
                    );
                }
                res = watcher_future => {
                    if let Err(err) = res {
                        crate::observability::logger::Logger::sys_error(
                            "worker.lifecycle",
                            &format!(
                                "Worker Pool ERROR: Dedicated Policy Watcher Worker {} crashed",
                                watcher_id
                            ),
                            &err.to_string(),
                        );
                        // Bắn tín hiệu hồi sinh về Lifecycle Manager
                        let _ = tx.send(WorkerSignal::RestartWorker(watcher_id)).await;
                    }
                }
            }
        });
    }

    /// Phát tín hiệu dừng đồng loạt cho toàn bộ các worker và đợi toàn bộ hoàn thành (Graceful Shutdown).
    pub async fn shutdown(&self) {
        self.cancel_token.cancel();
        crate::observability::logger::Logger::sys_info(
            "worker.lifecycle",
            "Worker Pool: Global cancellation token triggered. Waiting for all workers & tasks to gracefully complete...",
        );
        self.tracker.wait().await;
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
        while self.counter.load(Ordering::SeqCst) > 0 {
            self.notify.notified().await;
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
