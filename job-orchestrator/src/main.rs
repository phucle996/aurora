mod changefeed;
mod config;
mod contracts;
mod infra;
mod job_topics;
mod mail_runtime;
mod observability;
mod outbox;
mod reconcile;
mod results;
mod storage_usage;
mod workers;
mod zone_state;

use changefeed::ChangefeedWorker;
use config::Config;
use observability::logger::Logger;
use observability::metrics::MetricsManager;
use observability::otel::OtelTracer;
use results::ResultWorker;
use workers::RuntimeWorkers;

struct OtelShutdownGuard;

impl Drop for OtelShutdownGuard {
    fn drop(&mut self) {
        // Flush cả các lỗi bootstrap xảy ra sau khi provider đã được cài đặt.
        OtelTracer::stop();
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Thiết lập mặc định múi giờ TZ là Asia/Ho_Chi_Minh nếu chưa được định nghĩa
    if std::env::var("TZ").is_err() {
        std::env::set_var("TZ", "Asia/Ho_Chi_Minh");
    }

    // Logger lives longer than the OTel guard so provider shutdown diagnostics are flushed last.
    let _logger_guard = Logger::init();

    // 1. Nạp cấu hình từ biến môi trường đầu tiên để phục vụ khởi tạo Observability
    let mut config = match Config::load().await {
        Ok(cfg) => cfg,
        Err(err) => {
            Logger::sys_error("main.init", "Lỗi cấu hình biến môi trường", &err);
            return Err(std::io::Error::new(std::io::ErrorKind::InvalidInput, err).into());
        }
    };

    let vault = infra::vault::VaultClient::new(&config.vault).await?;
    infra::postgres::resolve_from_vault(&vault, &mut config.postgres).await?;
    infra::redis::resolve_from_vault(&vault, &mut config.shared_redis).await?;

    // Khởi tạo logger có cấu trúc, OpenTelemetry Tracer & Metrics (Push model)
    OtelTracer::init(&config.otel);
    let _otel_shutdown_guard = OtelShutdownGuard;
    MetricsManager::init();
    Logger::sys_info(
        "main.init",
        "Starting Job Orchestrator changefeed, result settlement and reconciliation workers",
    );

    Logger::sys_info(
        "main.init",
        &format!(
            "Cấu hình được nạp thành công: PG Slot={}, PG Publication={}, Kafka security={}, topic prefix={}",
            config.workflows.changefeed.slot_name,
            config.workflows.changefeed.publication_name,
            config.kafka.security_protocol,
            config.kafka.topic_prefix
        ),
    );

    // Runtime verifies but never mutates logical-replication infrastructure.
    changefeed::bootstrap::verify(&config).await?;

    // [COMMENT]: Shared Redis không còn chở Zone Job; chỉ giữ Central
    // reconciler/runtime bridge, bounded stream và lock/checkpoint.
    let cache_redis = infra::redis::client(&config.shared_redis)?;
    let kafka = infra::kafka::KafkaTransport::connect(&config.kafka)
        .await
        .map_err(std::io::Error::other)?;
    Logger::sys_info("main.init", "Đã khởi tạo Kafka transport và Shared Redis.");

    let nats_client = infra::nats::connect(&config.nats_core).await?;
    Logger::sys_info("main.init", "Đã kết nối thành công tới NATS Core.");
    let ownership_publisher = outbox::SharedStreamPublisher::connect(
        &cache_redis,
        &config.shared_redis,
        &config.workflows.ownership,
    )
    .await?;

    // ChangefeedWorker bootstraps its desired-state cache before WAL replay.
    let changefeed_worker = ChangefeedWorker::new(config.clone(), kafka.clone())
        .await
        .map_err(|e| -> Box<dyn std::error::Error> {
            format!("changefeed cache bootstrap failed: {e}").into()
        })?;
    let result_worker = ResultWorker::new(
        config.clone(),
        kafka.clone(),
        cache_redis.clone(),
        ownership_publisher.clone(),
    );
    let ownership_relay = outbox::OwnershipRelay::new(config.clone(), ownership_publisher);
    contracts::verify_generated_contracts();
    let runtime_workers = RuntimeWorkers::new(
        config.clone(),
        cache_redis.clone(),
        kafka.clone(),
        nats_client,
    );

    // [COMMENT]: Không gọi process::exit sau khi OTel đã khởi tạo; exit thô bỏ
    // toàn bộ batch queue. Mọi terminal branch hội tụ về một bounded shutdown.
    let run_result: Result<(), Box<dyn std::error::Error>> = tokio::select! {
        res = changefeed_worker.run() => {
            MetricsManager::record_worker_termination("changefeed");
            match res {
                Ok(()) => Err("changefeed worker stopped unexpectedly".into()),
                Err(error) => Err(format!("changefeed worker failed: {error}").into()),
            }
        }
        res = result_worker.run() => {
            MetricsManager::record_worker_termination("result_consumer");
            match res {
                Ok(()) => Err("result consumer stopped unexpectedly".into()),
                Err(error) => Err(format!("result consumer failed: {error}").into()),
            }
        }
        res = runtime_workers.run() => {
            MetricsManager::record_worker_termination("runtime_workers");
            match res {
                Ok(()) => Err("runtime workers stopped unexpectedly".into()),
                Err(error) => Err(format!("runtime workers failed: {error}").into()),
            }
        }
        res = ownership_relay.run() => {
            MetricsManager::record_worker_termination("ownership_relay");
            match res {
                Ok(()) => Err("ownership relay stopped unexpectedly".into()),
                Err(error) => Err(format!("ownership relay failed: {error}").into()),
            }
        }
        _ = reconcile::mail::run_periodic_mail_reconciliation(
            config.clone(), cache_redis.clone(), kafka.clone()
        ) => {
            MetricsManager::record_worker_termination("mail_reconciler");
            Err("mail reconciler stopped unexpectedly".into())
        }
        _ = MetricsManager::run_pipeline_sampler() => {
            MetricsManager::record_worker_termination("observability_sampler");
            Err("observability sampler stopped unexpectedly".into())
        }
        signal = shutdown_signal() => {
            match signal {
                Ok(()) => {
                    Logger::sys_info("main.shutdown", "Received SIGINT/SIGTERM; stopping workers");
                    Ok(())
                }
                Err(error) => Err(error),
            }
        }
    };

    if let Err(error) = &run_result {
        Logger::sys_error(
            "main.run",
            "Job Orchestrator terminal worker failure",
            &error.to_string(),
        );
    }
    Logger::record_pipeline_metrics();
    run_result
}

async fn shutdown_signal() -> Result<(), Box<dyn std::error::Error>> {
    use tokio::signal::unix::{signal, SignalKind};
    let mut sigint = signal(SignalKind::interrupt())?;
    let mut sigterm = signal(SignalKind::terminate())?;
    tokio::select! {
        _ = sigint.recv() => Ok(()),
        _ = sigterm.recv() => Ok(()),
    }
}
