use super::{mail, storage, zone};
use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use std::sync::Arc;

/// [COMMENT]: ReverseProvider chịu trách nhiệm lắng nghe và phản hồi các yêu cầu truy vấn ngược tài nguyên từ Dataplane.
pub struct ReverseProvider {
    config: Config,
    cache_redis: redis::Client,
    kafka: Arc<KafkaTransport>,
    nats_client: async_nats::Client,
}

impl ReverseProvider {
    /// Khởi tạo một ReverseProvider mới
    pub fn new(
        config: Config,
        cache_redis: redis::Client,
        kafka: Arc<KafkaTransport>,
        nats_client: async_nats::Client,
    ) -> Self {
        Self {
            config,
            cache_redis,
            kafka,
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
        let kafka_bp = self.kafka.clone();

        let config_md = self.config.clone();
        let kafka_md = self.kafka.clone();

        let config_st = self.config.clone();
        let kafka_st = self.kafka.clone();
        let nats_client_st = self.nats_client.clone();

        let config_mail_consumer = self.config.clone();
        let cache_redis_mail_consumer = self.cache_redis.clone();
        let nats_client_mail_consumer = self.nats_client.clone();

        // [COMMENT]: Chạy song song các listener độc lập; mail runtime reverse path dùng blocking
        // Redis consumer group riêng, không chia PEL với generic job result.
        tokio::select! {
            res = tokio::spawn(async move {
                loop {
                    {
                        let run_res = zone::listener::run_backpressure_listener(&config_bp, kafka_bp.clone()).await;
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
                        let run_res = zone::listener::run_metadata_query_listener(&config_md, kafka_md.clone()).await;
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
                        let run_res = storage::listener::run_bucket_sizes_listener(&config_st, kafka_st.clone(), &nats_client_st).await;
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
                            &cache_redis_mail_consumer,
                            &nats_client_mail_consumer,
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
        }

        Ok(())
    }
}
