use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

use crate::bootstrap::BootstrapResult;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::sqlite::SqliteDb;
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
    pub sqlite_db: SqliteDb,
    pub redis_job: Arc<RedisClientManager>,
    pub redis_internal_zone: Arc<RedisClientManager>,
    pub policy_engine: Arc<PolicyEngine>,
    pub worker_pool: Arc<WorkerLifecycleManager>,
}

impl AppContainer {
    /// Dựng đồ thị Module Graph từ kết quả bootstrap.
    pub fn new(boot: BootstrapResult) -> (Self, mpsc::Receiver<WorkerSignal>) {
        (
            Self {
                config: boot.config,
                sqlite_db: boot.sqlite_db,
                redis_job: boot.redis_job,
                redis_internal_zone: boot.redis_internal_zone,
                policy_engine: boot.policy_engine,
                worker_pool: boot.worker_pool,
            },
            boot.worker_signal_rx,
        )
    }

    /// Kích hoạt các luồng giám sát và tác vụ ngầm hoạt động (Watcher, Event loop).
    pub async fn start(&self, mut worker_signal_rx: mpsc::Receiver<WorkerSignal>) {
        // 0a. Khởi động tác vụ ngầm giám sát tài nguyên CPU/RAM hệ thống thô
        crate::observability::resource::ResourceMonitor::start_monitor();

        // 0e. Khởi chạy luồng giám sát sức khỏe kép (Dual-Path Liveness Heartbeat) định kỳ 5 giây
        let redis_job_hb = self.redis_job.clone();
        let zone_id_hb = self.config.zone_id.clone();
        let hostname_hb = crate::config::get_node_hostname();
        
        Logger::sys_info(
            "system.heartbeat",
            &format!("Starting Dataplane Dual-Path Heartbeat loop for node [{}] in zone [{}]", hostname_hb, zone_id_hb)
        );

        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            loop {
                interval.tick().await;

                // Thử nghiệm luồng chính: Đăng ký và ghi liveness cache lên Redis Job Broker
                match crate::infra::redis::query::register_node(redis_job_hb.client(), &zone_id_hb, &hostname_hb).await {
                    Ok(_) => {
                        match crate::infra::redis::query::send_liveness_heartbeat(redis_job_hb.client(), &zone_id_hb, &hostname_hb).await {
                            Ok(_) => {
                                Logger::sys_debug(
                                    "system.heartbeat",
                                    &format!("Successfully sent liveness cache heartbeat to Redis Job for node [{}]", hostname_hb)
                                );
                                continue; // Gửi luồng chính thành công, bỏ qua luồng dự phòng
                            }
                            Err(err) => {
                                Logger::sys_warn(
                                    "system.heartbeat",
                                    &format!("Failed to write liveness heartbeat to Redis Job: {}. Triggering fallback path...", err),
                                    "REDIS_HEARTBEAT_FAIL"
                                );
                            }
                        }
                    }
                    Err(err) => {
                        Logger::sys_warn(
                            "system.heartbeat",
                            &format!("Failed to register node in Redis Set: {}. Triggering fallback path...", err),
                            "REDIS_REGISTER_FAIL"
                        );
                    }
                }

                // Luồng dự phòng: Gọi gRPC Fallback Heartbeat trực tiếp lên Controlplane
                match crate::rpc::client::client::ExternalRpcSenderClient::send_fallback_heartbeat(&hostname_hb, &zone_id_hb).await {
                    Ok(_) => {
                        Logger::sys_info(
                            "system.heartbeat",
                            &format!("Successfully sent fallback gRPC heartbeat to Controlplane for node [{}]", hostname_hb)
                        );
                    }
                    Err(err) => {
                        Logger::sys_error(
                            "system.heartbeat",
                            &format!("CRITICAL: Both Main and Fallback heartbeat paths failed for node [{}]: {}", hostname_hb, err),
                            &err
                        );
                    }
                }
            }
        });


        // 0b. Khởi tạo 1 Worker ban đầu hoạt động nhận tin
        self.worker_pool
            .spawn_worker(
                1,
                self.config.clone(),
                self.policy_engine.clone(),
                self.redis_job.clone(),
                self.redis_internal_zone.clone(),
            )
            .await;

        // 0d. Khởi chạy luồng giám sát co giãn tự động động (AutoScaleWatcher) định kỳ 5 giây
        let config_scale = self.config.clone();
        let policy_engine_scale = self.policy_engine.clone();
        let worker_pool_scale = self.worker_pool.clone();
        let redis_job_scale = self.redis_job.clone();
        let redis_internal_zone_scale = self.redis_internal_zone.clone();

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

                // 3. Đánh giá tải thực tế
                let active_ids = worker_pool_scale.active_worker_ids();
                let current_count = active_ids.len();

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

        // 0c. Khởi động gRPC Server nội bộ của Dataplane dạng Stateless
        let grpc_port = self.config.dataplane_grpc_port;
        let grpc_tls_mode = self.config.dataplane_grpc_tls_mode;
        let grpc_ca = self.config.dataplane_grpc_ca_cert.clone();
        let grpc_cert = self.config.dataplane_grpc_client_cert.clone();
        let grpc_key = self.config.dataplane_grpc_client_key.clone();
        tokio::spawn(async move {
            let _ = crate::rpc::server::server::DataplaneGrpcServer::start_server(
                grpc_port,
                grpc_tls_mode,
                grpc_ca,
                grpc_cert,
                grpc_key,
            )
            .await;
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
        let policy_file = std::env::var("POLICY_FILE").unwrap_or_else(|_| "config/policy.yaml".to_string());
        let policy_path = PathBuf::from(&policy_file);
        let adapter = YamlFileAdapter::new(policy_path);

        self.worker_pool
            .spawn_dedicated_policy_watcher(0, move |token| async move {
                adapter
                    .start_watch(token, move || {
                        let path = PathBuf::from(
                            std::env::var("POLICY_FILE").unwrap_or_else(|_| "config/policy.yaml".to_string())
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
    }
}
