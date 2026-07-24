mod cdc;
mod config;
mod infra;
mod job_result;
mod lifecycle_relay;
mod observability;
mod reverse_provider;

use cdc::CdcStreamer;
use config::Config;
use job_result::JobResultConsumer;
use observability::logger::Logger;
use observability::metrics::MetricsManager;
use observability::otel::OtelTracer;
use reverse_provider::ReverseProvider;

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

    // 1. Nạp cấu hình từ biến môi trường đầu tiên để phục vụ khởi tạo Observability
    let config = match Config::from_env() {
        Ok(cfg) => cfg,
        Err(err) => {
            // Khởi động tạm logger thô để in lỗi cấu hình
            Logger::init();
            Logger::sys_error("main.init", "Lỗi cấu hình biến môi trường", &err);
            std::process::exit(1);
        }
    };

    // Khởi tạo logger có cấu trúc, OpenTelemetry Tracer & Metrics (Push model)
    Logger::init();
    OtelTracer::init(&config);
    let _otel_shutdown_guard = OtelShutdownGuard;
    MetricsManager::init();
    Logger::sys_info(
        "main.init",
        "Khởi động aurora-job-orchestrator (Mô hình 2 chiều)...",
    );

    Logger::sys_info(
        "main.init",
        &format!(
            "Cấu hình được nạp thành công: PG Slot={}, PG Publication={}, Kafka={}",
            config.slot_name, config.publication_name, config.kafka_bootstrap_servers
        ),
    );

    // 2. Khởi tạo và kiểm tra hạ tầng Logical Replication (Chạy một lần duy nhất lúc khởi động app, tự động reconnect)
    cdc::setup::setup_replication_infrastructure(&config).await?;

    // [COMMENT]: Shared Redis không còn chở Zone Job; chỉ giữ Central
    // reconciler/runtime bridge, bounded stream và lock/checkpoint.
    let cache_redis = redis::Client::open(config.shared_redis_url.clone())?;
    let kafka = infra::kafka::KafkaTransport::connect(&config)
        .await
        .map_err(std::io::Error::other)?;
    Logger::sys_info("main.init", "Đã khởi tạo Kafka transport và Shared Redis.");

    let nats_client = async_nats::connect(&config.env_nats_url).await?;
    Logger::sys_info(
        "main.init",
        &format!(
            "Đã kết nối thành công tới NATS Core tại: {}",
            config.env_nats_url
        ),
    );

    // 3. Khởi tạo các cấu phần proxy 2 chiều
    // [COMMENT]: CdcStreamer::new là async — bootstrap desired_state_cache từ DB trước khi run.
    // map_err để chuyển Box<dyn Error + Send + Sync> → Box<dyn Error> cho ? operator của main().
    let streamer = CdcStreamer::new(config.clone(), kafka.clone())
        .await
        .map_err(|e| -> Box<dyn std::error::Error> {
            format!("CDC bootstrap thất bại: {}", e).into()
        })?;
    let consumer = JobResultConsumer::new(config.clone(), kafka.clone(), cache_redis.clone());
    let reverse_provider = ReverseProvider::new(
        config.clone(),
        cache_redis.clone(),
        kafka.clone(),
        nats_client,
    );

    let db_url = config.database_url.clone();
    let nats_url = config.env_nats_url.clone();

    // [COMMENT]: Không gọi process::exit sau khi OTel đã khởi tạo; exit thô bỏ
    // toàn bộ batch queue. Mọi terminal branch hội tụ về một bounded shutdown.
    let run_result: Result<(), Box<dyn std::error::Error>> = tokio::select! {
        res = streamer.run() => {
            match res {
                Ok(()) => Err("CDC worker stopped unexpectedly".into()),
                Err(error) => Err(format!("CDC worker failed: {error}").into()),
            }
        }
        res = consumer.run() => {
            match res {
                Ok(()) => Err("result consumer stopped unexpectedly".into()),
                Err(error) => Err(format!("result consumer failed: {error}").into()),
            }
        }
        res = reverse_provider.run() => {
            match res {
                Ok(()) => Err("reverse provider stopped unexpectedly".into()),
                Err(error) => Err(format!("reverse provider failed: {error}").into()),
            }
        }
        _ = lifecycle_relay::relay::run_relay_loop(db_url, nats_url) => {
            Err("resource lifecycle relay stopped unexpectedly".into())
        }
        _ = reverse_provider::mail::reconciler::run_periodic_mail_reconciliation(
            config.clone(), cache_redis.clone(), kafka.clone()
        ) => {
            Err("mail reconciler stopped unexpectedly".into())
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
