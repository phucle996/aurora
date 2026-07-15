use dotenvy::dotenv;
use std::error::Error;
use std::io::Write;
use std::sync::Arc;
use tokio::sync::mpsc;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
// Đã loại bỏ PolicyEngine để đơn giản hóa kiến trúc Dataplane
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
    pub redis_job: Arc<RedisClientManager>,
    pub redis_internal_zone: Arc<RedisClientManager>,
    // Đã loại bỏ trường policy_engine trong BootstrapResult
    pub worker_pool: Arc<WorkerLifecycleManager>,
    pub worker_signal_rx: mpsc::Receiver<WorkerSignal>,
}

/// Khởi chạy chuỗi hành động bootstrap hạ tầng hệ thống.
pub fn run_actions() -> Result<BootstrapResult, Box<dyn Error>> {
    // 1. Load environment variables from .env
    dotenv().ok();

    // 2. Initialize structured logging system
    Logger::init();
    // Flush stdout ngay lập tức sau init để đảm bảo log hiển thị trong Docker non-TTY (block-buffered stdout).
    std::io::stdout().flush().ok();

    // 3. Load config from Environment
    let cfg = Config::load();
    Config::set_global(cfg.clone());
    Logger::sys_info(
        "system.bootstrap",
        &format!(
            "Starting stateless Aurora Dataplane in Zone={}...",
            cfg.zone_id
        ),
    );

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
            std::io::stdout().flush().ok();
            std::io::stderr().flush().ok();
            std::process::exit(1);
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
            std::io::stdout().flush().ok();
            std::io::stderr().flush().ok();
            std::process::exit(1);
        }
    };

    // Đã loại bỏ hoàn toàn phần đọc file policy.yaml và khởi tạo PolicyEngine ở đây.
    // max_workers sẽ được quản lý tĩnh qua biến môi trường nạp từ Config.

    // 8. Khởi tạo Worker Pool Lifecycle Manager kèm cấu hình Stalwart Host/Port từ env để dùng cho Connection Pool
    let (worker_pool, worker_signal_rx) = WorkerLifecycleManager::new(cfg.stalwart_lmtp_host.clone(), cfg.stalwart_lmtp_port);
    let worker_pool = Arc::new(worker_pool);

    Ok(BootstrapResult {
        config: Arc::new(cfg),
        redis_job: Arc::new(redis_job),
        redis_internal_zone: Arc::new(redis_internal_zone),
        worker_pool,
        worker_signal_rx,
    })
}
