use std::future::Future;
use std::panic::AssertUnwindSafe;
use std::sync::Arc;
use std::time::Duration;

use futures_util::FutureExt as _;

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
    pub kafka: Arc<crate::infra::kafka::KafkaTransport>,
    pub zone_kv: Arc<crate::infra::zone_kv::ZoneKvStore>,
    pub payload_keyring: Arc<crate::security::jobpayload::PayloadKeyring>,
    // Đã lược bỏ policy_engine khỏi AppContainer
    pub worker_pool: Arc<WorkerLifecycleManager>,
    pub job_execution_lease_registry:
        Arc<crate::job_runtime::coordination::watchdog::JobExecutionLeaseRegistry>,
    fatal_shutdown: tokio_util::sync::CancellationToken,
}

impl AppContainer {
    /// Dựng đồ thị Module Graph từ kết quả bootstrap.
    pub fn new(boot: BootstrapResult) -> Self {
        Self {
            config: boot.config,
            kafka: boot.kafka,
            zone_kv: boot.zone_kv,
            payload_keyring: boot.payload_keyring,
            worker_pool: boot.worker_pool,
            job_execution_lease_registry: Arc::new(
                crate::job_runtime::coordination::watchdog::JobExecutionLeaseRegistry::new(),
            ),
            fatal_shutdown: tokio_util::sync::CancellationToken::new(),
        }
    }

    pub fn fatal_shutdown_token(&self) -> tokio_util::sync::CancellationToken {
        self.fatal_shutdown.clone()
    }

    pub fn fence_jobs_for_process_restart(&self) {
        let cancelled = self
            .job_execution_lease_registry
            .cancel_all_for_process_restart();
        Logger::sys_error(
            "system.critical_task",
            &format!("Cancelled {cancelled} active job executions before critical-process restart"),
            "DATAPLANE_ACTIVE_JOBS_FENCED_FOR_RESTART",
        );
    }

    fn spawn_critical_task<F>(
        &self,
        task_name: &'static str,
        guard: crate::workerpool::pool::TaskGuard,
        future: F,
    ) where
        F: Future<Output = ()> + Send + 'static,
    {
        let normal_shutdown = self.worker_pool.cancel_token();
        let fatal_shutdown = self.fatal_shutdown.clone();
        tokio::spawn(async move {
            let _guard = guard;
            let outcome = AssertUnwindSafe(future).catch_unwind().await;
            if normal_shutdown.is_cancelled() {
                return;
            }
            Logger::sys_error(
                "system.critical_task",
                &format!(
                    "Critical task {task_name} {} before process shutdown",
                    if outcome.is_err() {
                        "panicked"
                    } else {
                        "exited"
                    }
                ),
                "DATAPLANE_CRITICAL_TASK_EXITED",
            );
            // Main observes this token and runs the same graceful shutdown path
            // before returning an error for the container supervisor to restart.
            fatal_shutdown.cancel();
        });
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

        // [COMMENT]: Health aggregate ở Zone KV; consumer runtime đi thẳng OTel/Victoria của Zone.
        crate::executor::mail::supervisor::MailWorkloadSupervisor::start_mail_runtime_observation(
            self.config.clone(),
            self.zone_kv.clone(),
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
            self.payload_keyring.clone(),
            self.worker_pool.track_task(),
        );

        // Zone Control owns all Zone-wide control workflows. Dataplane starts only
        // execution-local runtimes and consumes fenced directives produced by the
        // assigned Zone Control work units.

        // 0c. Khởi chạy luồng tự động gia hạn distributed lease lock (Watchdog Monitor) định kỳ 10 giây
        let registry = self.job_execution_lease_registry.clone();
        let zone_kv_watchdog = self.zone_kv.clone();
        let watchdog_shutdown = self.worker_pool.cancel_token();
        let watchdog_task_guard = self.worker_pool.track_task();
        let completion_capacity = self.config.max_workers.saturating_mul(2).clamp(16, 1_024);
        let (completion_tx, completion_rx) = tokio::sync::mpsc::channel(completion_capacity);
        let completion_kafka = self.kafka.clone();
        let completion_shutdown = self.worker_pool.cancel_token();
        let completion_guard = self.worker_pool.track_task();
        self.spawn_critical_task("job_completion_reporter", completion_guard, async move {
            crate::job_runtime::completion::run_completion_reporter(
                completion_rx,
                completion_kafka,
                completion_shutdown,
            )
            .await;
        });
        self.spawn_critical_task("job_execution_watchdog", watchdog_task_guard, async move {
            crate::job_runtime::coordination::watchdog::run_execution_watchdog(
                registry,
                zone_kv_watchdog,
                crate::job_runtime::coordination::lease::JOB_EXECUTION_LEASE_TTL_SECS,
                Duration::from_secs(10), // Quét gia hạn định kỳ mỗi 10 giây
                watchdog_shutdown,
                completion_tx,
                // Covers the 4x-ready settlement window plus one final poll
                // batch while the Kafka result reporter is backpressured.
                completion_capacity.saturating_add(32),
            )
            .await;
        });

        let retry_capacity = self.config.max_workers.saturating_mul(2).clamp(16, 1_024);
        let (retry_tx, retry_rx) = tokio::sync::mpsc::channel(retry_capacity);
        let retry_kafka = self.kafka.clone();
        let retry_shutdown = self.worker_pool.cancel_token();
        let retry_task_guard = self.worker_pool.track_task();
        self.spawn_critical_task("job_retry_scheduler", retry_task_guard, async move {
            crate::job_runtime::completion::run_retry_scheduler(
                retry_rx,
                retry_kafka,
                retry_shutdown,
            )
            .await;
        });

        // 0b. Khởi tạo bounded job channel và admitted_jobs counter dùng chung.
        let (tx, rx) = async_channel::bounded::<crate::job_runtime::model::QueuedJob>(
            self.config.job_queue_capacity,
        );
        let job_execution_runtime =
            Arc::new(crate::job_runtime::execution::JobExecutionRuntime::new(
                crate::job_runtime::execution::JobExecutionDependencies {
                    kafka: self.kafka.clone(),
                    zone_kv: self.zone_kv.clone(),
                    registry: self.job_execution_lease_registry.clone(),
                    admitted_jobs: admitted_jobs.clone(),
                    retry_tx,
                    zone_id: self.config.zone_id.clone(),
                    mail_runtime: self.worker_pool.mail_runtime.clone(),
                    hypervisor_runtime: self.worker_pool.hypervisor_runtime.clone(),
                    managed_service_runtime: self.worker_pool.managed_service_runtime.clone(),
                    cleanup_spawner: self.worker_pool.task_tracker(),
                    shutdown: self.worker_pool.cancel_token(),
                },
            ));
        let worker_runtime = Arc::new(WorkerJobRuntime::new(
            self.config.clone(),
            job_execution_runtime,
            rx,
        ));

        // [COMMENT]: Mỗi Dataplane chỉ consume command topic của đúng Zone.
        // Không nhận topic dùng chung vì nó phá vỡ routing và credential boundary giữa các Zone.
        let config_ingest_zone = self.config.clone();
        let kafka_ingest_zone = self.kafka.clone();
        let zone_kv_ingest_zone = self.zone_kv.clone();
        let tx_ingest_zone = tx;
        let admitted_jobs_ingest_zone = admitted_jobs.clone();
        let execution_capacity = self.worker_pool.clone();
        let payload_keyring_ingest_zone = self.payload_keyring.clone();
        let cancel_token_zone = self.worker_pool.cancel_token();
        let zone_ingestion_guard = self.worker_pool.track_task();

        self.spawn_critical_task("zone_job_intake", zone_ingestion_guard, async move {
            crate::job_runtime::intake::run_zone_job_intake(
                config_ingest_zone,
                kafka_ingest_zone,
                zone_kv_ingest_zone,
                tx_ingest_zone,
                cancel_token_zone,
                admitted_jobs_ingest_zone,
                execution_capacity,
                payload_keyring_ingest_zone,
            )
            .await;
        });

        // Bootstrap the configured baseline immediately; the assigned Zone
        // Control directive is an optimization signal and may be stale during
        // reassignment.
        for worker_id in 1..=self.config.min_workers {
            self.worker_pool
                .spawn_worker(worker_id, worker_runtime.clone());
        }

        // [COMMENT]: Worker không tự quyết định scale. Nó chỉ apply directive có
        // assignment epoch và TTL từ AURORA_ZONE_COORDINATION; directive stale
        // thì giữ capacity hiện tại.
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
