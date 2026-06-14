use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

use crate::bootstrap::BootstrapResult;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use crate::policyengine::adapter::YamlFileAdapter;
use crate::policyengine::engine::PolicyEngine;
use crate::policyengine::types::PolicySet;
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
    pub policy_engine: Arc<PolicyEngine>,
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
                policy_engine: boot.policy_engine,
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
        // 0a. Khởi động tác vụ ngầm giám sát tài nguyên CPU/RAM hệ thống thô
        crate::observability::resource::ResourceMonitor::start_monitor();

        // 0b. Khởi chạy máy chủ HTTP scrape metrics cho Prometheus
        crate::workerpool::metrics::PromRegistry::init(self.config.metrics_port);

        // 0d. Khởi tạo OpenTelemetry tracer pipeline kết nối tới Tempo
        crate::observability::otel::OtelTracer::init();

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



        // 0b. Khởi tạo 1 Worker ban đầu hoạt động nhận tin
        self.worker_pool
            .spawn_worker(
                1,
                self.config.clone(),
                self.policy_engine.clone(),
                self.redis_job.clone(),
                self.redis_internal_zone.clone(),
                self.active_lock_registry.clone(),
            )
            .await;

        // 0d. Khởi chạy luồng giám sát co giãn tự động động (AutoScaleWatcher) định kỳ 5 giây
        let config_scale = self.config.clone();
        let policy_engine_scale = self.policy_engine.clone();
        let worker_pool_scale = self.worker_pool.clone();
        let redis_job_scale = self.redis_job.clone();
        let redis_internal_zone_scale = self.redis_internal_zone.clone();
        let active_lock_registry_scale = self.active_lock_registry.clone();

        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            loop {
                interval.tick().await;

                // 1. Trích xuất max_workers cấu hình động từ Policy Engine
                let max_workers = policy_engine_scale
                    .current()
                    .policies
                    .get("max_workers")
                    .and_then(|v| v.as_u64())
                    .unwrap_or(100) as usize;

                let auto_scaler = crate::workerpool::auto_scale::AutoScaleEngine::new(max_workers);

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

                // Ghi nhận các chỉ số đo đạc thu được vào Prometheus Registry phục vụ giám sát HA
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
                                    policy_engine_scale.clone(),
                                    redis_job_scale.clone(),
                                    redis_internal_zone_scale.clone(),
                                    active_lock_registry_scale.clone(),
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

        // 2. Kích hoạt Dedicated Policy Watcher Worker thông qua Worker Pool
        let policy_engine_clone = self.policy_engine.clone();
        let policy_file =
            std::env::var("POLICY_FILE").unwrap_or_else(|_| "config/policy.yaml".to_string());
        let policy_path = PathBuf::from(&policy_file);
        let adapter = YamlFileAdapter::new(policy_path);

        self.worker_pool
            .spawn_dedicated_policy_watcher(0, move |token| async move {
                adapter
                    .start_watch(token, move || {
                        let path = PathBuf::from(
                            std::env::var("POLICY_FILE")
                                .unwrap_or_else(|_| "config/policy.yaml".to_string()),
                        );
                        if let Ok(raw_yaml) = std::fs::read_to_string(&path) {
                            let checksum = PolicySet::calculate_checksum(&raw_yaml);
                            if let Ok(mut new_policy) = serde_yaml::from_str::<PolicySet>(&raw_yaml)
                            {
                                new_policy.checksum_sha = checksum;
                                if let Err(err) = policy_engine_clone.swap(new_policy) {
                                    Logger::sys_warn(
                                        "policyengine.reload",
                                        "Failed to swap policy snapshot, keeping Last-Known-Good",
                                        &err,
                                    );
                                }
                            }
                        }
                    })
                    .await
            })
            .await;

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
