mod config;
mod cdc;
mod result_consumer;
mod observability;
mod reverse_provider;

use config::Config;
use cdc::CdcStreamer;
use result_consumer::ResultConsumer;
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

    // 3. Khởi tạo kết nối Redis
    let redis_client = redis::Client::open(config.redis_url.clone())?;
    Logger::sys_info("main.init", "Đã khởi tạo Redis Client thành công.");

    // 3. Khởi tạo các cấu phần proxy 2 chiều
    let streamer = CdcStreamer::new(config.clone(), redis_client.clone());
    let consumer = ResultConsumer::new(config.clone(), redis_client.clone());
    let reverse_provider = ReverseProvider::new(config.clone(), redis_client.clone());

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

