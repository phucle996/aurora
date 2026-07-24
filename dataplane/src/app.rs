use std::sync::Arc;
use std::time::Duration;

use crate::bootstrap::BootstrapResult;
use crate::config::Config;
use crate::observability::logger::Logger;
use crate::workerpool::pool::WorkerLifecycleManager;
use crate::workerpool::runtime::WorkerJobRuntime;

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
    pub job_execution_lease_registry:
        Arc<crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry>,
}

impl AppContainer {
    /// Dựng đồ thị Module Graph từ kết quả bootstrap.
    pub fn new(boot: BootstrapResult) -> Self {
        Self {
            config: boot.config,
            nats_core: boot.nats_core,
            kafka: boot.kafka,
            zone_kv: boot.zone_kv,
            worker_pool: boot.worker_pool,
            job_execution_lease_registry: Arc::new(
                crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry::new(),
            ),
        }
    }

    /// Kích hoạt các luồng giám sát và tác vụ ngầm hoạt động (Watcher, Event loop).
    pub async fn start(&self) {
        // 0b. Khởi tạo OpenTelemetry (Traces & Metrics) kết nối tới OTel Collector
        crate::observability::otel::OtelTracer::init(&self.config);
        crate::observability::metrics::NodeRuntimeMetrics::init_registry();
        crate::observability::metrics::JobExecutionMetrics::init_registry();
        crate::observability::metrics::WorkerControlMetrics::init_registry();

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
        let node_hostname = hostname::get()
            .map(|h| h.to_string_lossy().into_owned())
            .unwrap_or_else(|_| uuid::Uuid::new_v4().to_string());
        // A boot suffix prevents a restarted StatefulSet pod from sharing a
        // health key with a paused incarnation that can resume later.
        let node_id = format!("{node_hostname}-{}", uuid::Uuid::new_v4());

        let admitted_jobs = Arc::new(std::sync::atomic::AtomicUsize::new(0));

        // Khởi động NodeRuntimeSampler; mỗi node ghi snapshot riêng vào Zone health KV.
        crate::observability::metrics::NodeRuntimeSampler::start_node_runtime_sampler(
            node_id,
            self.zone_kv.clone(),
            self.worker_pool.clone(),
            self.kafka.clone(),
            admitted_jobs.clone(),
            self.worker_pool.track_task(),
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
        let registry = self.job_execution_lease_registry.clone();
        let zone_kv_watchdog = self.zone_kv.clone();
        let watchdog_shutdown = self.worker_pool.cancel_token();
        let watchdog_task_guard = self.worker_pool.track_task();
        let timeout_report_capacity = self.config.max_workers.saturating_mul(2).clamp(16, 1_024);
        let (timeout_report_tx, timeout_report_rx) =
            tokio::sync::mpsc::channel(timeout_report_capacity);
        let timeout_reporter_kafka = self.kafka.clone();
        let timeout_reporter_shutdown = self.worker_pool.cancel_token();
        let timeout_reporter_guard = self.worker_pool.track_task();
        tokio::spawn(async move {
            let _timeout_reporter_guard = timeout_reporter_guard;
            crate::job_lifecycle::timeout_reporter::run_execution_timeout_reporter(
                timeout_report_rx,
                timeout_reporter_kafka,
                timeout_reporter_shutdown,
            )
            .await;
        });
        tokio::spawn(async move {
            let _watchdog_task_guard = watchdog_task_guard;
            crate::workerpool::lease_watchdog::run_job_execution_lease_watchdog(
                registry,
                zone_kv_watchdog,
                crate::job_lifecycle::lease::JOB_EXECUTION_LEASE_TTL_SECS,
                Duration::from_secs(10), // Quét gia hạn định kỳ mỗi 10 giây
                watchdog_shutdown,
                timeout_report_tx,
            )
            .await;
        });

        let lease_retry_capacity = self.config.max_workers.saturating_mul(2).clamp(16, 1_024);
        let (job_execution_lease_retry_tx, job_execution_lease_retry_rx) =
            tokio::sync::mpsc::channel(lease_retry_capacity);
        let lease_retry_kafka = self.kafka.clone();
        let lease_retry_shutdown = self.worker_pool.cancel_token();
        let lease_retry_task_guard = self.worker_pool.track_task();
        tokio::spawn(async move {
            let _lease_retry_task_guard = lease_retry_task_guard;
            crate::job_lifecycle::lease::run_job_execution_lease_retry_publisher(
                job_execution_lease_retry_rx,
                lease_retry_kafka,
                lease_retry_shutdown,
            )
            .await;
        });

        // 0b. Khởi tạo bounded job channel và admitted_jobs counter dùng chung.
        let (tx, rx) = async_channel::bounded::<crate::job_lifecycle::message::JobPayload>(
            self.config.job_queue_capacity,
        );
        let worker_runtime = Arc::new(WorkerJobRuntime::new(
            self.config.clone(),
            self.kafka.clone(),
            self.zone_kv.clone(),
            self.job_execution_lease_registry.clone(),
            rx,
            admitted_jobs.clone(),
            job_execution_lease_retry_tx,
        ));

        // [COMMENT]: Mỗi Dataplane chỉ consume command topic của đúng Zone.
        // Không nhận topic dùng chung vì nó phá vỡ routing và credential boundary giữa các Zone.
        let config_ingest_zone = self.config.clone();
        let kafka_ingest_zone = self.kafka.clone();
        let zone_kv_ingest_zone = self.zone_kv.clone();
        let tx_ingest_zone = tx;
        let admitted_jobs_ingest_zone = admitted_jobs.clone();
        let cancel_token_zone = self.worker_pool.cancel_token();
        let zone_ingestion_guard = self.worker_pool.track_task();

        tokio::spawn(async move {
            let _zone_ingestion_guard = zone_ingestion_guard;
            crate::job_lifecycle::consumer::JobConsumer::start_zone_ingestion(
                config_ingest_zone,
                kafka_ingest_zone,
                zone_kv_ingest_zone,
                tx_ingest_zone,
                cancel_token_zone,
                admitted_jobs_ingest_zone,
            )
            .await;
        });

        // Bootstrap the configured baseline immediately; the leader directive
        // is an optimization signal and may be stale during failover.
        for worker_id in 1..=self.config.min_workers {
            self.worker_pool
                .spawn_worker(worker_id, worker_runtime.clone());
        }

        // [COMMENT]: Worker không tự quyết định scale. Nó chỉ apply directive có leader fencing
        // và TTL từ AURORA_ZONE_COORDINATION; directive stale thì giữ capacity hiện tại.
        crate::workerpool::scale_follower::start_worker_scale_directive_follower(
            self.worker_pool.clone(),
            worker_runtime,
            self.worker_pool.track_task(),
        );

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
