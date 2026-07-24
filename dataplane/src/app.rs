use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

use crate::bootstrap::BootstrapResult;
use crate::config::Config;
use crate::observability::logger::Logger;
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
///   - Bảo vệ Worker Pool và các cổng I/O (Watcher, Kafka, NATS) khỏi việc phơi bày trực tiếp ra ngoài.
///
pub struct AppContainer {
    pub config: Arc<Config>,
    pub nats_core: Arc<crate::infra::nats_core::NatsCoreTransport>,
    pub kafka: Arc<crate::infra::kafka::KafkaTransport>,
    pub zone_kv: Arc<crate::infra::zone_kv::ZoneKvStore>,
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
                nats_core: boot.nats_core,
                kafka: boot.kafka,
                zone_kv: boot.zone_kv,
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

        // [COMMENT]: Phase 5 hydrate/watch NATS KV trước; Phase 6 supervisor chỉ đọc immutable COW snapshots.
        self.worker_pool
            .mail_runtime
            .configuration
            .start_mail_configuration_runtime_projection(self.zone_kv.clone());
        self.worker_pool
            .mail_runtime
            .consumer_supervisor
            .start_mail_consumer_runtime_supervisor();

        // [COMMENT]: Health aggregate ở Zone KV; consumer runtime realtime đi NATS Core và không chạm Redis.
        crate::executor::mail::supervisor::MailWorkloadSupervisor::start_mail_runtime_reporting(
            self.config.clone(),
            self.zone_kv.clone(),
            self.nats_core.clone(),
            self.worker_pool.mail_runtime.clone(),
            self.worker_pool.cancel_token(),
        );

        // Sinh node_id độc nhất cho instance Dataplane này (dùng hostname hoặc uuid làm fallback)
        let node_id = hostname::get()
            .map(|h| h.to_string_lossy().into_owned())
            .unwrap_or_else(|_| uuid::Uuid::new_v4().to_string());

        // Khởi động ResourceMonitor; mỗi node ghi snapshot riêng vào Zone health KV.
        crate::observability::resource::ResourceMonitor::start_dataplane_resource_snapshot_writer(
            node_id,
            self.zone_kv.clone(),
            self.worker_pool.clone(),
            self.kafka.clone(),
        );

        // [COMMENT]: Một entry point duy nhất sở hữu election và mọi Zone-wide singleton duty.
        crate::leader::ZoneLeaderSupervisor::start_zone_leader_supervisor(
            self.config.clone(),
            self.zone_kv.clone(),
            self.kafka.clone(),
            self.worker_pool.mail_runtime.clone(),
            self.worker_pool.cancel_token(),
            self.worker_pool.track_task(),
        );

        // 0c. Khởi chạy luồng tự động gia hạn distributed lease lock (Watchdog Monitor) định kỳ 10 giây
        let registry = self.active_lock_registry.clone();
        let zone_kv_watchdog = self.zone_kv.clone();
        let kafka_watchdog = self.kafka.clone();
        tokio::spawn(async move {
            crate::workerpool::watchdog::start_watchdog_loop(
                registry,
                zone_kv_watchdog,
                kafka_watchdog,
                30,                      // TTL Zone KV lease là 30 giây
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
        let kafka_ingest_zone = self.kafka.clone();
        let zone_kv_ingest_zone = self.zone_kv.clone();
        let tx_ingest_zone = tx.clone();
        let active_jobs_ingest_zone = active_jobs.clone();
        let cancel_token_zone = self.worker_pool.cancel_token();
        let zone_topic = kafka_ingest_zone.zone_command_topic(&config_ingest_zone.zone_id);
        let zone_group = format!("aurora-dataplane-zone-{}-v1", config_ingest_zone.zone_id);

        tokio::spawn(async move {
            crate::job_lifecycle::consumer::JobConsumer::start_ingestion(
                config_ingest_zone,
                kafka_ingest_zone,
                zone_kv_ingest_zone,
                tx_ingest_zone,
                cancel_token_zone,
                active_jobs_ingest_zone,
                zone_topic,
                zone_group,
            )
            .await;
        });

        let config_ingest_plat = self.config.clone();
        let kafka_ingest_plat = self.kafka.clone();
        let zone_kv_ingest_plat = self.zone_kv.clone();
        let tx_ingest_plat = tx;
        let active_jobs_ingest_plat = active_jobs.clone();
        let cancel_token_plat = self.worker_pool.cancel_token();

        tokio::spawn(async move {
            // [COMMENT]: Trì hoãn 500ms để tránh tranh chấp khóa (LeveledRwLock) khi 2 consumer kafka cùng khởi tạo song song.
            tokio::time::sleep(Duration::from_millis(500)).await;
            crate::job_lifecycle::consumer::JobConsumer::start_ingestion(
                config_ingest_plat,
                kafka_ingest_plat.clone(),
                zone_kv_ingest_plat,
                tx_ingest_plat,
                cancel_token_plat,
                active_jobs_ingest_plat,
                kafka_ingest_plat.platform_command_topic(),
                "aurora-dataplane-platform-v1".to_string(),
            )
            .await;
        });

        // 0d. Khởi tạo 1 Worker ban đầu hoạt động xử lý tin từ channel
        self.worker_pool
            .spawn_worker(
                1,
                self.config.clone(),
                self.kafka.clone(),
                self.zone_kv.clone(),
                self.active_lock_registry.clone(),
                rx_shared.clone(),
                active_jobs.clone(),
            )
            .await;

        // [COMMENT]: Worker không tự quyết định scale. Nó chỉ apply directive có leader fencing
        // và TTL từ AURORA_ZONE_COORDINATION; directive stale thì giữ capacity hiện tại.
        crate::workerpool::scale_follower::start_worker_scale_follower(
            self.config.clone(),
            self.worker_pool.clone(),
            self.kafka.clone(),
            self.zone_kv.clone(),
            self.active_lock_registry.clone(),
            rx_shared,
            active_jobs,
        );

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
