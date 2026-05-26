use std::path::PathBuf;
use std::sync::Arc;
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

        // 0b. Khởi chạy vòng lặp Ingestion của JobConsumer bất đồng bộ
        let policy_engine_ingest = self.policy_engine.clone();
        let worker_pool_ingest = self.worker_pool.clone();
        let redis_job_ingest = self.redis_job.clone();
        tokio::spawn(async move {
            crate::job_receiver::consumer::JobConsumer::start_ingestion(
                policy_engine_ingest,
                worker_pool_ingest,
                redis_job_ingest,
            )
            .await;
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
        let worker_pool_clone = self.worker_pool.clone();
        tokio::spawn(async move {
            while let Some(signal) = worker_signal_rx.recv().await {
                match signal {
                    WorkerSignal::Shutdown => {
                        worker_pool_clone.shutdown();
                    }
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
        let policy_path = PathBuf::from("config/policy.yaml");
        let adapter = YamlFileAdapter::new(policy_path);

        self.worker_pool
            .spawn_dedicated_policy_watcher(0, move |token| async move {
                adapter
                    .start_watch(token, move || {
                        let path = PathBuf::from("config/policy.yaml");
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
    pub fn stop(&self) {
        Logger::sys_info(
            "system.shutdown",
            "Stopping application container gracefully...",
        );
        self.worker_pool.shutdown();
    }
}
