use super::{mail, storage, zone};
use crate::config::Config;
use crate::observability::logger::Logger;

/// [COMMENT]: ReverseProvider chịu trách nhiệm lắng nghe và phản hồi các yêu cầu truy vấn ngược tài nguyên từ Dataplane.
pub struct ReverseProvider {
    config: Config,
    redis_client: redis::Client,
    nats_client: async_nats::Client,
}

impl ReverseProvider {
    /// Khởi tạo một ReverseProvider mới
    pub fn new(
        config: Config,
        redis_client: redis::Client,
        nats_client: async_nats::Client,
    ) -> Self {
        Self {
            config,
            redis_client,
            nats_client,
        }
    }

    /// Khởi chạy vòng lặp lắng nghe PubSub với cơ chế tự động reconnect
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "reverse_provider.run",
            "ReverseProvider: Khởi chạy luồng xử lý truy vấn ngược tài nguyên và Backpressure (Bypass CP)...",
        );

        let config_bp = self.config.clone();
        let redis_client_bp = self.redis_client.clone();

        let config_md = self.config.clone();
        let redis_client_md = self.redis_client.clone();

        let config_st = self.config.clone();
        let redis_client_st = self.redis_client.clone();
        let nats_client_st = self.nats_client.clone();

        let config_mail_consumer = self.config.clone();
        let redis_client_mail_consumer = self.redis_client.clone();
        let config_mail_infra = self.config.clone();
        let redis_client_mail_infra = self.redis_client.clone();

        // [COMMENT]: Chạy song song các listener độc lập; mail runtime reverse path dùng blocking
        // Redis consumer group riêng, không chia PEL với generic job result.
        tokio::select! {
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
            res = tokio::spawn(async move {
                loop {
                    {
                        let run_res = storage::listener::run_bucket_sizes_listener(&config_st, &redis_client_st, &nats_client_st).await;
                        if let Err(e) = run_res {
                            let err_msg = e.to_string();
                            Logger::sys_error(
                                "reverse_provider.storage_sizes_listener",
                                "Storage Bucket Sizes Listener gặp lỗi, tiến hành kết nối lại sau 5s...",
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
                        let run_res = mail::reporter::consumer::run_consumer_report_listener(
                            &config_mail_consumer,
                            &redis_client_mail_consumer,
                        ).await;
                        if let Err(error) = run_res {
                            Logger::sys_error(
                                "reverse_provider.mail_consumer_report",
                                "Mail Consumer Report Listener failed; reconnecting after 5s",
                                &error.to_string(),
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
                        let run_res = mail::reporter::infrastructure::run_infra_report_listener(
                            &config_mail_infra,
                            &redis_client_mail_infra,
                        ).await;
                        if let Err(error) = run_res {
                            Logger::sys_error(
                                "reverse_provider.mail_infra_report",
                                "Mail Infrastructure Report Listener failed; reconnecting after 5s",
                                &error.to_string(),
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
