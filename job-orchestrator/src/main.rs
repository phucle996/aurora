mod config;
mod cdc;
mod job_result;
mod observability;
mod reverse_provider;

use config::Config;
use cdc::CdcStreamer;
use job_result::JobResultConsumer;
use observability::logger::Logger;
use observability::otel::OtelTracer;
use observability::metrics::MetricsManager;
use reverse_provider::ReverseProvider;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
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
    MetricsManager::init();
    Logger::sys_info("main.init", "Khởi động aurora-job-orchestrator (Mô hình 2 chiều)...");

    Logger::sys_info(
        "main.init",
        &format!(
            "Cấu hình được nạp thành công: PG Slot={}, PG Publication={}, Result Stream={}",
            config.slot_name, config.publication_name, config.result_stream_name
        ),
    );

    // 2. Khởi tạo và kiểm tra hạ tầng Logical Replication (Chạy một lần duy nhất lúc khởi động app, tự động reconnect)
    cdc::setup::setup_replication_infrastructure(&config).await?;

    // 3. Khởi tạo kết nối Redis & NATS
    let redis_client = redis::Client::open(config.redis_url.clone())?;
    Logger::sys_info("main.init", "Đã khởi tạo Redis Client thành công.");

    let nats_client = async_nats::connect(&config.env_nats_url).await?;
    Logger::sys_info("main.init", &format!("Đã kết nối thành công tới NATS Core tại: {}", config.env_nats_url));

    // 3. Khởi tạo các cấu phần proxy 2 chiều
    // [COMMENT]: CdcStreamer::new là async — bootstrap desired_state_cache từ DB trước khi run.
    // map_err để chuyển Box<dyn Error + Send + Sync> → Box<dyn Error> cho ? operator của main().
    let streamer = CdcStreamer::new(config.clone(), redis_client.clone())
        .await
        .map_err(|e| -> Box<dyn std::error::Error> { format!("CDC bootstrap thất bại: {}", e).into() })?;
    let consumer = JobResultConsumer::new(config.clone(), redis_client.clone(), nats_client.clone());
    let reverse_provider = ReverseProvider::new(config.clone(), redis_client.clone(), nats_client);

    // 4. Chạy song song các luồng nền độc lập (HA)
    tokio::select! {
        res = streamer.run() => {
            if let Err(err) = res {
                Logger::sys_error("main.run", "CDC Worker (Outbound) gặp lỗi nghiêm trọng", &err.to_string());
                std::process::exit(1);
            }
        }
        res = consumer.run() => {
            if let Err(err) = res {
                Logger::sys_error("main.run", "Result Consumer (Inbound) gặp lỗi nghiêm trọng", &err.to_string());
                std::process::exit(1);
            }
        }
        res = reverse_provider.run() => {
            if let Err(err) = res {
                Logger::sys_error("main.run", "Reverse Provider gặp lỗi nghiêm trọng", &err.to_string());
                std::process::exit(1);
            }
        }
    }

    Ok(())
}

