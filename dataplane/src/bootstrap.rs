use dotenvy::dotenv;
use std::error::Error;
use std::sync::Arc;

use crate::config::Config;
use crate::observability::logger::Logger;
// Đã loại bỏ PolicyEngine để đơn giản hóa kiến trúc Dataplane
use crate::workerpool::pool::WorkerLifecycleManager;

/// ============================================================================
/// 📂 MODULE: bootstrap.rs - Khởi Tạo Hạ Tầng Hệ Thống Dataplane (Bootstrapping)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Nạp các tệp cấu hình môi trường (.env), logger hệ thống.
///   - Mở Kafka transport, NATS Core và NATS JetStream KV dạng fail-fast.
///   - Trả về đồ thị tài nguyên `BootstrapResult` để dựng AppContainer.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hàm `run_actions` là luồng khởi tạo duy nhất cho toàn bộ Dataplane.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kiểm soát tính hợp lệ của cấu hình hệ thống ngay khi boot.
///   - Nếu Kafka, NATS Core hoặc Zone KV lỗi, bootstrap thất bại trước khi nhận workload.
///
pub struct BootstrapResult {
    pub config: Arc<Config>,
    pub nats_core: Arc<crate::infra::nats_core::NatsCoreTransport>,
    pub kafka: Arc<crate::infra::kafka::KafkaTransport>,
    pub zone_kv: Arc<crate::infra::zone_kv::ZoneKvStore>,
    // Đã loại bỏ trường policy_engine trong BootstrapResult
    pub worker_pool: Arc<WorkerLifecycleManager>,
}

/// Khởi chạy chuỗi hành động bootstrap hạ tầng hệ thống.
pub async fn run_actions() -> Result<BootstrapResult, Box<dyn Error>> {
    // 1. Load environment variables from .env
    dotenv().ok();

    // 2. Logger is owned by main so its writer guard outlives bootstrap and every background task.

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

    // [COMMENT]: Kafka fail-fast để pod không nhận workload khi chưa có durable result/retry transport.
    let kafka = crate::infra::kafka::KafkaTransport::connect(&cfg)
        .await
        .map_err(|error| format!("initialize Kafka transport failed: {error}"))?;

    // [COMMENT]: Dataplane không còn credential Redis trung tâm; watch/report realtime chỉ đi NATS Core.
    let nats_core = crate::infra::nats_core::NatsCoreTransport::connect(&cfg)
        .await
        .map_err(|error| format!("initialize NATS Core transport failed: {error}"))?;
    nats_core
        .start_watch_listener()
        .await
        .map_err(|error| format!("initialize NATS Core runtime watch failed: {error}"))?;

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
    let hypervisor_runtime =
        crate::executor::hypervisor::HypervisorRuntime::new(&cfg, zone_kv.clone())
            .map_err(|error| format!("initialize Hypervisor runtime failed: {error}"))?;
    let worker_pool = Arc::new(WorkerLifecycleManager::new(
        mail_runtime,
        hypervisor_runtime,
    ));

    Ok(BootstrapResult {
        config: Arc::new(cfg),
        nats_core,
        kafka,
        zone_kv,
        worker_pool,
    })
}
