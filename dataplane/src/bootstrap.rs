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
///   - Mở Kafka transport, Redis runtime cache và NATS JetStream KV dạng fail-fast.
///   - Trả về đồ thị tài nguyên `BootstrapResult` để dựng AppContainer.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hàm `run_actions` là luồng khởi tạo duy nhất cho toàn bộ Dataplane.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kiểm soát tính hợp lệ của cấu hình hệ thống ngay khi boot.
///   - Nếu Kafka, Redis runtime hoặc Zone KV lỗi, bootstrap thất bại trước khi nhận workload.
///
pub struct BootstrapResult {
    pub config: Arc<Config>,
    pub runtime_redis: Arc<RedisClientManager>,
    pub kafka: Arc<crate::infra::kafka::KafkaTransport>,
    pub zone_kv: Arc<crate::infra::zone_kv::ZoneKvStore>,
    // Đã loại bỏ trường policy_engine trong BootstrapResult
    pub worker_pool: Arc<WorkerLifecycleManager>,
    pub worker_signal_rx: mpsc::Receiver<WorkerSignal>,
}

/// Khởi chạy chuỗi hành động bootstrap hạ tầng hệ thống.
pub async fn run_actions() -> Result<BootstrapResult, Box<dyn Error>> {
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

    // [COMMENT]: Redis này chỉ giữ runtime lease/snapshot có TTL; durable job đã chuyển sang Kafka.
    let runtime_redis = match RedisClientManager::new(
        &cfg.runtime_redis_url,
        cfg.runtime_redis_tls_mode,
        &cfg.runtime_redis_ca_cert,
        &cfg.runtime_redis_client_cert,
        &cfg.runtime_redis_client_key,
    ) {
        Ok(r) => r,
        Err(err) => {
            Logger::sys_error(
                "system.bootstrap",
                "CRITICAL: Failed to initialize runtime Cache Redis client connection pool",
                &err,
            );
            std::io::stdout().flush().ok();
            std::io::stderr().flush().ok();
            std::process::exit(1);
        }
    };

    // [COMMENT]: Kafka fail-fast để pod không nhận workload khi chưa có durable result/retry transport.
    let kafka = crate::infra::kafka::KafkaTransport::connect(&cfg)
        .await
        .map_err(|error| format!("initialize Kafka transport failed: {error}"))?;

    // [COMMENT]: Toàn bộ shared Zone state/lease nằm trong JetStream KV; Dataplane không bootstrap Internal Redis.
    let zone_kv =
        crate::infra::zone_kv::ZoneKvStore::connect(&cfg.nats_zone_url, cfg.nats_zone_kv_replicas)
            .await
            .map_err(|error| format!("initialize Zone NATS KV failed: {error}"))?;

    // Đã loại bỏ hoàn toàn phần đọc file policy.yaml và khởi tạo PolicyEngine ở đây.
    // max_workers sẽ được quản lý tĩnh qua biến môi trường nạp từ Config.

    // [COMMENT]: JMAP client + batcher được tạo đúng một lần cho toàn pod; cấu hình/auth sai làm bootstrap fail-fast.
    let mail_runtime = crate::executor::mail::MailRuntime::new(&cfg, zone_kv.clone())
        .map_err(|error| format!("initialize JMAP mail runtime failed: {error}"))?;
    let (worker_pool, worker_signal_rx) = WorkerLifecycleManager::new(mail_runtime);
    let worker_pool = Arc::new(worker_pool);

    Ok(BootstrapResult {
        config: Arc::new(cfg),
        runtime_redis: Arc::new(runtime_redis),
        kafka,
        zone_kv,
        worker_pool,
        worker_signal_rx,
    })
}
