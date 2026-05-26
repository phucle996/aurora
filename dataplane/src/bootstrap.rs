use dotenvy::dotenv;
use std::error::Error;
use std::fs;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::mpsc;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::sqlite::SqliteDb;
use crate::observability::logger::Logger;
use crate::policyengine::engine::PolicyEngine;
use crate::policyengine::types::PolicySet;
use crate::workerpool::lifecycle::{WorkerLifecycleManager, WorkerSignal};

/// ============================================================================
/// 📂 MODULE: bootstrap.rs - Khởi Tạo Hạ Tầng Hệ Thống Dataplane (Bootstrapping)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Nạp các tệp cấu hình môi trường (.env), logger hệ thống.
///   - Mở các kết nối hạ tầng cốt lõi (SQLite, Redis Job, Redis Policy) dạng fail-fast.
///   - Trả về đồ thị tài nguyên `BootstrapResult` để dựng AppContainer.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hàm `run_actions` là luồng khởi tạo duy nhất cho toàn bộ Dataplane.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kiểm soát tính hợp lệ của cấu hình hệ thống ngay khi boot.
///   - Nếu bất kỳ hạ tầng thiết yếu nào bị lỗi (SQLite, Redis), lập tức gọi abort tiến trình.
///
pub struct BootstrapResult {
    pub config: Arc<Config>,
    pub sqlite_db: SqliteDb,
    pub redis_job: Arc<RedisClientManager>,
    pub redis_internal_zone: Arc<RedisClientManager>,
    pub policy_engine: Arc<PolicyEngine>,
    pub worker_pool: Arc<WorkerLifecycleManager>,
    pub worker_signal_rx: mpsc::Receiver<WorkerSignal>,
}

/// Khởi chạy chuỗi hành động bootstrap hạ tầng hệ thống.
pub fn run_actions() -> Result<BootstrapResult, Box<dyn Error>> {
    // 1. Load environment variables from .env
    dotenv().ok();

    // 2. Initialize structured logging system
    Logger::init();

    // 3. Load config from Environment
    let cfg = Config::load();
    Logger::sys_info(
        "system.bootstrap",
        &format!(
            "Starting stateless Aurora Dataplane in Zone={}...",
            cfg.zone_id
        ),
    );

    // 4. Initialize local SQLite Database for Idempotency
    let db = match SqliteDb::init_connection("/var/lib/dataplane/idempotency.db") {
        Ok(d) => d,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                "CRITICAL: Failed to initialize local SQLite database connection pool",
                &err,
            );
            std::process::abort();
        }
    };

    // 5. Initialize Job Queue Redis connection pool
    let redis_job = match RedisClientManager::new(
        &cfg.redis_job_url,
        cfg.redis_job_tls_mode,
        &cfg.redis_job_ca_cert,
        &cfg.redis_job_client_cert,
        &cfg.redis_job_client_key,
    ) {
        Ok(r) => r,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                "CRITICAL: Failed to initialize Job Queue Redis client connection pool",
                &err,
            );
            std::process::abort();
        }
    };

    // 6. Initialize Internal Zone Redis connection pool
    let redis_internal_zone = match RedisClientManager::new(
        &cfg.redis_internal_zone_url,
        cfg.redis_internal_zone_tls_mode,
        &cfg.redis_internal_zone_ca_cert,
        &cfg.redis_internal_zone_client_cert,
        &cfg.redis_internal_zone_client_key,
    ) {
        Ok(r) => r,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                "CRITICAL: Failed to initialize Internal Zone Redis client connection pool",
                &err,
            );
            std::process::abort();
        }
    };

    // 7. Load and Parse Initial YAML Policy file
    let policy_path = PathBuf::from("config/policy.yaml");
    let initial_yaml = match fs::read_to_string(&policy_path) {
        Ok(s) => s,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                &format!(
                    "CRITICAL: Failed to read initial policy file at {:?}",
                    policy_path
                ),
                &err.to_string(),
            );
            std::process::abort();
        }
    };

    let mut initial_policy: PolicySet = match serde_yaml::from_str(&initial_yaml) {
        Ok(p) => p,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                "CRITICAL: Failed to parse initial YAML policy file",
                &err.to_string(),
            );
            std::process::abort();
        }
    };

    // Calculate dynamic checksum on bootstrap
    initial_policy.checksum_sha = PolicySet::calculate_checksum(&initial_yaml);

    // 8. Instantiate PolicyEngine in-memory lock-free snapshot manager
    let policy_engine = Arc::new(PolicyEngine::new(initial_policy));
    let active_policy = policy_engine.current();
    Logger::sys_info(
        "system.bootstrap",
        &format!(
            "Policy Engine initialized successfully. Active policy version: {}, checksum: {}",
            active_policy.version, active_policy.checksum_sha
        ),
    );

    // 9. Initialize Worker Pool Lifecycle Manager
    let (worker_pool, worker_signal_rx) = WorkerLifecycleManager::new();
    let worker_pool = Arc::new(worker_pool);

    Ok(BootstrapResult {
        config: Arc::new(cfg),
        sqlite_db: db,
        redis_job: Arc::new(redis_job),
        redis_internal_zone: Arc::new(redis_internal_zone),
        policy_engine,
        worker_pool,
        worker_signal_rx,
    })
}
