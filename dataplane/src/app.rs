use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

use crate::bootstrap::BootstrapResult;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
// Đã lược bỏ các import liên quan đến policyengine
use crate::workerpool::lifecycle::{WorkerLifecycleManager, WorkerSignal};

/// ============================================================================
/// 📂 MODULE: app.rs - Bộ Quản Lý Đồ Thị Đối Tượng Ứng Dụng (AppContainer)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Container quản lý đồ thị đối tượng ứng dụng (Dependency Graph).
///   - Quản lý vòng đời khởi động/tắt (start/stop) các dịch vụ ngầm có tính hệ thống.
///   - Tương đương 100% với kiến trúc `internal/app/module.go` của Controlplane.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Thực thể `AppContainer` duy trì quyền sở hữu các tài nguyên dùng chung trong suốt vòng đời tiến trình.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Đóng gói toàn bộ cơ cấu đấu nối dây (wiring) giữa các thành phần.
///   - Bảo vệ Worker Pool và các cổng I/O (Watcher, Redis, DB) khỏi việc phơi bày trực tiếp ra ngoài.
///
pub struct AppContainer {
    pub config: Arc<Config>,
    pub redis_job: Arc<RedisClientManager>,
    pub redis_internal_zone: Arc<RedisClientManager>,
    // Đã lược bỏ policy_engine khỏi AppContainer
    pub worker_pool: Arc<WorkerLifecycleManager>,
    pub active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
}

impl AppContainer {
    /// Dựng đồ thị Module Graph từ kết quả bootstrap.
    pub fn new(boot: BootstrapResult) -> (Self, mpsc::Receiver<WorkerSignal>) {
        (
            Self {
                config: boot.config,
                redis_job: boot.redis_job,
                redis_internal_zone: boot.redis_internal_zone,
                worker_pool: boot.worker_pool,
                active_lock_registry: Arc::new(
                    crate::workerpool::watchdog::ActiveLockRegistry::new(),
                ),
            },
            boot.worker_signal_rx,
        )
    }

    /// Kích hoạt các luồng giám sát và tác vụ ngầm hoạt động (Watcher, Event loop).
    pub async fn start(&self, mut worker_signal_rx: mpsc::Receiver<WorkerSignal>) {
        // 0b. Khởi tạo OpenTelemetry (Traces & Metrics) kết nối tới OTel Collector
        crate::observability::otel::OtelTracer::init(&self.config);
        crate::workerpool::metrics::WorkerMetricsManager::init_registry();

        // Khởi động Mail Workload Watchdog giám sát Stalwart L2 (HA & Decoupled)
        crate::executor::mail::monitor::MailWorkloadMonitor::start(
            self.config.clone(),
            self.redis_internal_zone.clone(),
            self.worker_pool.mail_runtime.clone(),
        );

        // [COMMENT]: Khởi động Storage Workload Watchdog giám sát MinIO L2 (HA & Decoupled)
        crate::executor::storage::StorageWorkloadMonitor::start(
            self.config.clone(),
            self.redis_internal_zone.clone(),
        );

        // [COMMENT]: Khởi động luồng quét dung lượng bucket định kỳ mỗi 15s
        crate::executor::storage::StorageSizesSyncer::start(
            self.config.clone(),
            self.redis_internal_zone.clone(),
            self.redis_job.clone(),
        );

        // Sinh node_id độc nhất cho instance Dataplane này (dùng hostname hoặc uuid làm fallback)
        let node_id = hostname::get()
            .map(|h| h.to_string_lossy().into_owned())
            .unwrap_or_else(|_| uuid::Uuid::new_v4().to_string());

        // Khởi động ResourceMonitor kiêm Node Reporter báo cáo CPU/RAM và workers lên Redis L2 (Self-Healing L2 Node Reporter)
        crate::observability::resource::ResourceMonitor::start_monitor(
            node_id,
            self.redis_internal_zone.clone(),
            self.worker_pool.clone(),
        );

        // Khởi động HypervisorMonitor polling Proxmox Cluster API mỗi 15 giây (Luồng B Auto-Discovery)
        // Monitor này ghi trạng thái node vật lý vào Redis L2 `infra:hypervisor` để ZoneStatusGateway tổng hợp.
        // Nếu PROXMOX_API_URL hoặc PROXMOX_API_TOKEN chưa set, monitor tự degraded gracefully.
        crate::executor::hypervisor::core::monitor::HypervisorMonitor::start(
            self.config.clone(),
            self.redis_internal_zone.clone(),
        );

        // Khởi động Zone Gateway tổng hợp dữ liệu cụm L2 và đồng bộ lên Platform L1 (Bypass CP)
        crate::zone_gateway::ZoneStatusGateway::start_zone_gateway(
            self.redis_internal_zone.clone(),
            self.redis_job.clone(),
            self.config.clone(),
        );

        // Khởi động CDC Metadata Event Listener lắng nghe các sự kiện cập nhật cấu hình thời gian thực
        crate::zone_gateway::ZoneStatusGateway::start_metadata_event_listener(
            self.redis_internal_zone.clone(),
            self.redis_job.clone(),
            self.config.clone(),
        );

        // 0c. Khởi chạy luồng tự động gia hạn distributed lease lock (Watchdog Monitor) định kỳ 10 giây
        let registry = self.active_lock_registry.clone();
        let redis_internal = self.redis_internal_zone.clone();
        tokio::spawn(async move {
            crate::workerpool::watchdog::start_watchdog_loop(
                registry,
                redis_internal,
                30,                      // TTL gia hạn trên Redis là 30 giây
                Duration::from_secs(10), // Quét gia hạn định kỳ mỗi 10 giây
            )
            .await;
        });

        // 0b. Khởi tạo Kênh truyền tin và Bộ đếm active_jobs dùng chung
        let (tx, rx) = tokio::sync::mpsc::channel::<crate::job_lifecycle::message::JobPayload>(100);
        let rx_shared = Arc::new(tokio::sync::Mutex::new(rx));
        let active_jobs = Arc::new(std::sync::atomic::AtomicUsize::new(0));

        // 0c. [COMMENT]: Khởi chạy dual persistent IngestionDaemons: một cho Zone, một cho Platform
        let config_ingest_zone = self.config.clone();
        let redis_job_ingest_zone = self.redis_job.clone();
        let redis_internal_zone_ingest_zone = self.redis_internal_zone.clone();
        let tx_ingest_zone = tx.clone();
        let active_jobs_ingest_zone = active_jobs.clone();
        let cancel_token_zone = self.worker_pool.cancel_token();
        let zone_stream = format!("jobs:{}", config_ingest_zone.zone_id);

        tokio::spawn(async move {
            crate::job_lifecycle::consumer::JobConsumer::start_ingestion(
                config_ingest_zone,
                redis_job_ingest_zone,
                redis_internal_zone_ingest_zone,
                tx_ingest_zone,
                cancel_token_zone,
                active_jobs_ingest_zone,
                zone_stream,
                "dataplane-group".to_string(),
            )
            .await;
        });

        let config_ingest_plat = self.config.clone();
        let redis_job_ingest_plat = self.redis_job.clone();
        let redis_internal_zone_ingest_plat = self.redis_internal_zone.clone();
        let tx_ingest_plat = tx;
        let active_jobs_ingest_plat = active_jobs.clone();
        let cancel_token_plat = self.worker_pool.cancel_token();

        tokio::spawn(async move {
            crate::job_lifecycle::consumer::JobConsumer::start_ingestion(
                config_ingest_plat,
                redis_job_ingest_plat,
                redis_internal_zone_ingest_plat,
                tx_ingest_plat,
                cancel_token_plat,
                active_jobs_ingest_plat,
                "jobs:platform".to_string(),
                "dataplane-platform-group".to_string(),
            )
            .await;
        });

        // 0d. Khởi tạo 1 Worker ban đầu hoạt động xử lý tin từ channel
        self.worker_pool
            .spawn_worker(
                1,
                self.config.clone(),
                self.redis_job.clone(),
                self.redis_internal_zone.clone(),
                self.active_lock_registry.clone(),
                rx_shared.clone(),
                active_jobs.clone(),
            )
            .await;

        // 0e. Khởi chạy luồng giám sát co giãn tự động động (AutoScaleWatcher) định kỳ 5 giây
        let config_scale = self.config.clone();
        let worker_pool_scale = self.worker_pool.clone();
        let redis_job_scale = self.redis_job.clone();
        let redis_internal_zone_scale = self.redis_internal_zone.clone();
        let active_lock_registry_scale = self.active_lock_registry.clone();
        let rx_scale = rx_shared.clone();
        let active_jobs_scale = active_jobs.clone();

        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            loop {
                interval.tick().await;

                // 1. Trích xuất min_workers và max_workers cấu hình tĩnh trực tiếp từ Config
                let min_workers = config_scale.min_workers;
                let max_workers = config_scale.max_workers;

                let auto_scaler =
                    crate::workerpool::auto_scale::AutoScaleEngine::new(min_workers, max_workers);

                // 2. Thu thập chỉ số hàng đợi thực tế từ Redis
                let stream_key = format!("jobs:{}", config_scale.zone_id);
                let lag = crate::infra::redis::query::query_stream_lag(
                    redis_job_scale.client(),
                    &stream_key,
                )
                .await
                .unwrap_or(0);
                let latency = crate::infra::redis::query::query_stream_latency_ms(
                    redis_job_scale.client(),
                    &stream_key,
                )
                .await
                .unwrap_or(0.0);
                let active_conns = 0; // Thống kê kết nối giả lập

                // Ghi nhận các chỉ số đo đạc thu được vào OpenTelemetry Registry phục vụ giám sát HA
                crate::workerpool::metrics::WorkerMetricsManager::record_metrics(
                    crate::workerpool::metrics::MetricsType::RedisStreamLag {
                        zone_id: config_scale.zone_id.clone(),
                        lag,
                    },
                );

                // 3. Đánh giá tải thực tế
                let active_ids = worker_pool_scale.active_worker_ids();
                let current_count = active_ids.len();

                crate::workerpool::metrics::WorkerMetricsManager::record_metrics(
                    crate::workerpool::metrics::MetricsType::ActiveConnectionsCount {
                        zone_id: config_scale.zone_id.clone(),
                        count: current_count,
                    },
                );

                let target_count =
                    auto_scaler.evaluate_scale(current_count, lag, latency, active_conns);

                // Chỉ log khi target khác với current (có scale up/down thực sự).
                // Khi hệ thống đứng yên ở cùng mức worker, autoscaler im lặng để tránh log spam.
                if target_count != current_count {
                    Logger::sys_info(
                        "worker.scaler",
                        &format!(
                            "Autoscaler scaling: {} -> {} workers (lag={}, latency={:.2}ms)",
                            current_count, target_count, lag, latency
                        ),
                    );
                }

                if target_count > current_count {
                    // Scale Up: Spawn thêm worker
                    for i in 1..=target_count {
                        if !active_ids.contains(&i) {
                            worker_pool_scale
                                .spawn_worker(
                                    i,
                                    config_scale.clone(),
                                    redis_job_scale.clone(),
                                    redis_internal_zone_scale.clone(),
                                    active_lock_registry_scale.clone(),
                                    rx_scale.clone(),
                                    active_jobs_scale.clone(),
                                )
                                .await;
                        }
                    }
                } else if target_count < current_count {
                    // Scale Down: Terminate bớt worker (ID lớn nhất)
                    let mut sorted_ids = active_ids.clone();
                    sorted_ids.sort_by(|a, b| b.cmp(a));

                    let diff = current_count - target_count;
                    for i in 0..diff {
                        if let Some(&worker_id) = sorted_ids.get(i) {
                            worker_pool_scale.terminate_worker(worker_id);
                        }
                    }
                }
            }
        });

        // 1. Khởi chạy luồng giám sát sự cố/hoạt động của Worker Pool
        tokio::spawn(async move {
            while let Some(signal) = worker_signal_rx.recv().await {
                match signal {
                    WorkerSignal::RestartWorker(id) => {
                        Logger::sys_warn(
                            "workerpool.lifecycle",
                            &format!("Worker {} crashed/panic, restarting...", id),
                            "Panic/Crash Detected",
                        );
                    }
                }
            }
        });

        Logger::sys_info(
            "system.bootstrap",
            "Application module graph successfully initialized and running.",
        );
    }

    /// Dừng an toàn (Graceful Shutdown) toàn bộ các luồng công việc đang thực thi.
    pub async fn stop(&self) {
        Logger::sys_info(
            "system.shutdown",
            "Stopping application container gracefully...",
        );
        self.worker_pool.shutdown().await;
        // Giải phóng và flush toàn bộ trace spans còn sót lại lên collector
        crate::observability::otel::OtelTracer::stop();
    }
}
