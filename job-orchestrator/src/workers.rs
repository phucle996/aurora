use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use crate::{mail_runtime, storage_usage, zone_state};
use std::hash::{Hash, Hasher};
use std::sync::Arc;
use std::time::Duration;

pub struct RuntimeWorkers {
    config: Config,
    cache_redis: redis::Client,
    kafka: Arc<KafkaTransport>,
    nats_client: async_nats::Client,
}

impl RuntimeWorkers {
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

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "workers.run",
            "Starting Zone state, storage usage and mail runtime workers",
        );

        let zone_config = self.config.clone();
        let zone_kafka = self.kafka.clone();
        let zone_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) =
                    zone_state::worker::run_backpressure_listener(&zone_config, zone_kafka.clone())
                        .await
                {
                    Logger::sys_error(
                        "workers.zone_state",
                        "Zone state worker stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("zone_state", &mut failures).await;
            }
        };

        let metadata_config = self.config.clone();
        let metadata_kafka = self.kafka.clone();
        let metadata_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) = zone_state::metadata::run_metadata_query_listener(
                    &metadata_config,
                    metadata_kafka.clone(),
                )
                .await
                {
                    Logger::sys_error(
                        "workers.zone_metadata",
                        "Zone metadata worker stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("zone_metadata", &mut failures).await;
            }
        };

        let storage_config = self.config.clone();
        let storage_kafka = self.kafka.clone();
        let storage_redis = self.cache_redis.clone();
        let storage_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) = storage_usage::worker::run_bucket_sizes_listener(
                    &storage_config,
                    storage_kafka.clone(),
                    &storage_redis,
                )
                .await
                {
                    Logger::sys_error(
                        "workers.storage_usage",
                        "Storage usage worker stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("storage_usage", &mut failures).await;
            }
        };

        let reports_config = self.config.clone();
        let reports_redis = self.cache_redis.clone();
        let reports_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) = mail_runtime::reports::run_consumer_report_listener(
                    &reports_config,
                    &reports_redis,
                )
                .await
                {
                    Logger::sys_error(
                        "workers.mail_reports",
                        "Mail runtime report worker stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("mail_reports", &mut failures).await;
            }
        };

        let ingest_redis = self.cache_redis.clone();
        let ingest_nats = self.nats_client.clone();
        let ingest_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) = mail_runtime::ingest::run_runtime_report_nats_bridge(
                    &ingest_redis,
                    &ingest_nats,
                )
                .await
                {
                    Logger::sys_error(
                        "workers.mail_ingest",
                        "Mail runtime NATS to Redis bridge stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("mail_ingest", &mut failures).await;
            }
        };

        let watch_config = self.config.clone();
        let watch_redis = self.cache_redis.clone();
        let watch_nats = self.nats_client.clone();
        let watch_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) = mail_runtime::watch::run_runtime_watch_bridge(
                    &watch_config,
                    &watch_redis,
                    &watch_nats,
                )
                .await
                {
                    Logger::sys_error(
                        "workers.mail_watch",
                        "Mail runtime Redis to NATS bridge stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("mail_watch", &mut failures).await;
            }
        };

        let watchdog_config = self.config.clone();
        let watchdog_redis = self.cache_redis.clone();
        let watchdog_worker = async move {
            let mut failures = 0_u32;
            loop {
                if let Err(error) =
                    zone_state::watchdog::run(watchdog_config.clone(), watchdog_redis.clone()).await
                {
                    Logger::sys_error(
                        "workers.zone_watchdog",
                        "Zone watchdog stopped; retrying",
                        &error.to_string(),
                    );
                }
                retry_delay("zone_watchdog", &mut failures).await;
            }
        };

        // These are owned futures, not detached tasks. Dropping RuntimeWorkers
        // on SIGTERM drops every broker/DB connection before OTel shutdown.
        tokio::select! {
            _ = zone_worker => {}
            _ = metadata_worker => {}
            _ = storage_worker => {}
            _ = reports_worker => {}
            _ = ingest_worker => {}
            _ = watch_worker => {}
            _ = watchdog_worker => {}
        }
        Ok(())
    }
}

async fn retry_delay(worker: &'static str, failures: &mut u32) {
    *failures = failures.saturating_add(1);
    let exponential_seconds = 1_u64 << (*failures).min(5);
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    crate::config::get_node_hostname().hash(&mut hasher);
    worker.hash(&mut hasher);
    failures.hash(&mut hasher);
    let jitter_ms = hasher.finish() % 1_000;
    tokio::time::sleep(
        Duration::from_secs(exponential_seconds.min(30)) + Duration::from_millis(jitter_ms),
    )
    .await;
}
