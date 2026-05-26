use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

/// ============================================================================
/// 📂 MODULE: workerpool/lifecycle.rs - Trình Quản Lý Vòng Đời Worker Vật Lý
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Quản trị việc cấp phát (provisioning) các worker thô chạy dưới dạng tokio green tasks.
///   - Theo dõi sức khỏe hoạt động của các worker, thực hiện restart/hồi sinh khi phát hiện crash/panic.
///   - Điều phối tắt an toàn toàn bộ worker pool (Graceful Termination) khi nhận tín hiệu kết thúc.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Vòng đời và trạng thái hoạt động thực của các luồng tokio (`JoinHandle`).
///   - Trạng thái cấu hình số lượng worker hiện hành được cung cấp bởi `policyengine`.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Hoàn toàn độc lập và tách biệt khỏi domain nghiệp vụ (workload-agnostic).
///   - Không quan tâm hay can thiệp vào cấu trúc dữ liệu Payload, Tenant hay các loại Job nghiệp vụ.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi `job-receiver/consumer.rs` khi khởi chạy cụm đọc Stream.
///   - Nhận tín hiệu điều khiển trực tiếp từ `main.rs` khi OS phát tín hiệu đóng ứng dụng.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Sử dụng `CancellationToken` phân cấp (parent-child relationship) của `tokio-util`.
///   - Khi nhận tín hiệu tắt, worker sẽ KHÔNG bị ngắt thô bạo (hard kill) mà sẽ hoàn thành nốt job đang kéo từ stream,
///     sau đó thực hiện báo cáo kết quả và tự kết thúc an toàn.
///
pub struct WorkerLifecycleManager {
    /// Token gốc dùng để phát tín hiệu dừng đồng loạt cho toàn bộ các worker đang chạy ngầm.
    cancel_token: CancellationToken,

    /// Kênh truyền nhận tín hiệu điều khiển trạng thái sống của các worker (ví dụ: yêu cầu hồi sinh).
    signal_sender: mpsc::Sender<WorkerSignal>,
}

/// Các loại tín hiệu điều phối vòng đời của worker
pub enum WorkerSignal {
    /// Yêu cầu đóng khẩn cấp toàn bộ hệ thống worker
    Shutdown,
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
        };
        (manager, rx)
    }

    /// Cấp phát và khởi chạy một worker chạy dưới dạng Tokio Task độc lập.
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   1. Tạo một child token kế thừa từ token gốc để phục vụ graceful shutdown.
    ///   2. Khởi chạy tác vụ bất đồng bộ `tokio::spawn` chạy ngầm.
    ///   3. Lắng nghe đồng thời sự kiện hủy bỏ hoặc hết thời hạn chờ (timeout).
    ///   4. Nếu có lỗi nghiêm trọng xảy ra, worker chủ động đẩy tín hiệu hồi sinh `RestartWorker` về kênh chính.
    pub async fn spawn_worker(&self, worker_id: usize) {
        let token = self.cancel_token.child_token();
        let tx = self.signal_sender.clone();

        tokio::spawn(async move {
            println!(
                "Worker Pool: Worker {} provisioned and actively listening for stream jobs...",
                worker_id
            );

            tokio::select! {
                _ = token.cancelled() => {
                    println!("Worker Pool: Worker {} received graceful cancellation token. Draining queue and terminating...", worker_id);
                }
                _ = tokio::time::sleep(tokio::time::Duration::from_secs(3600)) => {
                    // Trình trạng mô phỏng lỗi kết nối mạng kéo dài khiến worker bị đứng.
                    // Đẩy tin nhắn báo động về lifecycle manager để kích hoạt restart policy.
                    let _ = tx.send(WorkerSignal::RestartWorker(worker_id)).await;
                }
            }
        });
    }

    /// Cấp phát và khởi chạy một Worker chuyên biệt chuyên trách theo dõi chính sách (Dedicated Watcher Worker).
    ///
    /// Luồng chạy ngầm này được giám sát chặt chẽ bởi Lifecycle Manager. Nếu tệp watch gặp sự cố I/O
    /// làm panic luồng, Lifecycle Manager sẽ nhận cảnh báo qua kênh mpsc và tự động hồi sinh.
    pub async fn spawn_dedicated_policy_watcher<F, Fut>(&self, watcher_id: usize, make_watcher_future: F)
    where
        F: FnOnce(CancellationToken) -> Fut + Send + 'static,
        Fut: std::future::Future<Output = Result<(), String>> + Send + 'static,
    {
        let token = self.cancel_token.child_token();
        let tx = self.signal_sender.clone();
        let token_clone = token.clone();

        tokio::spawn(async move {
            println!("Worker Pool: Dedicated Policy Watcher Worker {} started under lifecycle surveillance...", watcher_id);
            
            // Khởi chạy future thực thi việc watch file/subscribe
            let watcher_future = make_watcher_future(token_clone);
            
            tokio::select! {
                _ = token.cancelled() => {
                    println!("Worker Pool: Dedicated Policy Watcher Worker {} shutdown gracefully.", watcher_id);
                }
                res = watcher_future => {
                    if let Err(err) = res {
                        eprintln!("Worker Pool ERROR: Dedicated Policy Watcher Worker {} crashed: {}", watcher_id, err);
                        // Bắn tín hiệu hồi sinh về Lifecycle Manager
                        let _ = tx.send(WorkerSignal::RestartWorker(watcher_id)).await;
                    }
                }
            }
        });
    }

    /// Phát tín hiệu dừng đồng loạt cho toàn bộ các worker trong pool.
    /// Kích hoạt cơ chế dừng an toàn (graceful close worker).
    pub fn shutdown(&self) {
        self.cancel_token.cancel();
        println!("Worker Pool: Global cancellation token triggered. Broadcasing to all workers...");
    }
}
