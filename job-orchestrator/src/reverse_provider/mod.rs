pub mod decision;
pub mod hypervisor;
pub mod mail;
pub mod storage;
pub mod zone;


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
            "ReverseProvider: Khởi chạy luồng xử lý truy vấn ngược tài nguyên và Backpressure (Bypass CP)...",
        );

        let config_svc = self.config.clone();
        let redis_client_svc = self.redis_client.clone();

        let config_bp = self.config.clone();
        let redis_client_bp = self.redis_client.clone();

        let config_md = self.config.clone();
        let redis_client_md = self.redis_client.clone();

        // Chạy song song cả ba luồng lắng nghe độc lập (HA & Fault Tolerance)
        tokio::select! {
            res = tokio::spawn(async move {
                loop {
                    {
                        let run_res = mail::listener::run_listener(&config_svc, &redis_client_svc).await;
                        if let Err(e) = run_res {
                            let err_msg = e.to_string();
                            Logger::sys_error(
                                "reverse_provider.mail_listener",
                                "Template Listener gặp lỗi, tiến hành kết nối lại sau 5s...",
                                &err_msg
                            );
                        }
                    }
                    tokio::time::sleep(std::time::Duration::from_secs(5)).await;
                }
            }) => {
                let _ = res;
            }
            res = tokio::spawn(async move {
                loop {
                    {
                        let run_res = zone::listener::run_backpressure_listener(&config_bp, &redis_client_bp).await;
                        if let Err(e) = run_res {
                            let err_msg = e.to_string();
                            Logger::sys_error(
                                "reverse_provider.backpressure_listener",
                                "Backpressure Listener gặp lỗi, tiến hành kết nối lại sau 5s...",
                                &err_msg
                            );
                        }
                    }
                    tokio::time::sleep(std::time::Duration::from_secs(5)).await;
                }
            }) => {
                let _ = res;
            }
            res = tokio::spawn(async move {
                loop {
                    {
                        let run_res = zone::listener::run_metadata_query_listener(&config_md, &redis_client_md).await;
                        if let Err(e) = run_res {
                            let err_msg = e.to_string();
                            Logger::sys_error(
                                "reverse_provider.metadata_listener",
                                "Metadata Query Listener gặp lỗi, tiến hành kết nối lại sau 5s...",
                                &err_msg
                            );
                        }
                    }
                    tokio::time::sleep(std::time::Duration::from_secs(5)).await;
                }
            }) => {
                let _ = res;
            }
        }

        Ok(())
    }
}
