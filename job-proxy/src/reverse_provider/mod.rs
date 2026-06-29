pub mod listener;
pub mod dispatcher;
pub mod mail;

use crate::config::Config;
use crate::observability::logger::Logger;

/// ReverseProvider chịu trách nhiệm lắng nghe và phản hồi các yêu cầu truy vấn ngược tài nguyên từ Dataplane.
pub struct ReverseProvider {
    config: Config,
    redis_client: redis::Client,
}

impl ReverseProvider {
    /// Khởi tạo một ReverseProvider mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self {
            config,
            redis_client,
        }
    }

    /// Khởi chạy vòng lặp lắng nghe PubSub với cơ chế tự động reconnect
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "reverse_provider.run",
            "ReverseProvider: Khởi chạy luồng xử lý truy vấn ngược tài nguyên...",
        );

        loop {
            if let Err(e) = listener::run_listener(&self.config, &self.redis_client).await {
                Logger::sys_error(
                    "reverse_provider.run",
                    "ReverseProvider: Luồng xử lý gặp lỗi. Tiến hành kết nối lại sau 5 giây...",
                    &e.to_string()
                );
                tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            } else {
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
            }
        }
    }
}
