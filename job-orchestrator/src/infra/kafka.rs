use crate::config::{KafkaConfig, KafkaSecurityProtocol, TlsTrustSource};
use ahash::AHashMap;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{AutoOffsetReset, Consumer, OffsetAndMetadata, TopicPartition};
use krafka::producer::{Acks, Producer};
use krafka::protocol::Compression;
use prost::Message;
use std::sync::Arc;
use std::time::{Duration, Instant};

pub mod transport_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.transport.v1.rs"));
}

pub struct KafkaTransport {
    producer: Arc<Producer>,
    bootstrap_servers: String,
    auth: Option<AuthConfig>,
    topic_prefix: String,
    client_id: String,
    request_timeout: Duration,
    metadata_max_age: Duration,
    consumer_max_poll_records: i32,
    consumer_session_timeout: Duration,
    consumer_heartbeat_interval: Duration,
    publish_attempts: u32,
    publish_retry_delay: Duration,
}

impl KafkaTransport {
    pub async fn connect(config: &KafkaConfig) -> Result<Arc<Self>, String> {
        let auth = match config.security_protocol {
            KafkaSecurityProtocol::Plaintext => None,
            KafkaSecurityProtocol::Ssl => Some(AuthConfig::ssl(kafka_tls(config)?)),
            KafkaSecurityProtocol::SaslPlaintext => Some(
                AuthConfig::sasl_plain(
                    config
                        .username
                        .as_deref()
                        .ok_or("KAFKA_USERNAME is required")?,
                    config
                        .password
                        .as_deref()
                        .ok_or("KAFKA_PASSWORD is required")?,
                )
                .map_err(|error| format!("invalid Kafka SASL config: {error}"))?,
            ),
            KafkaSecurityProtocol::SaslPlainSsl => Some(
                AuthConfig::sasl_plain_ssl(
                    config
                        .username
                        .as_deref()
                        .ok_or("KAFKA_USERNAME is required")?,
                    config
                        .password
                        .as_deref()
                        .ok_or("KAFKA_PASSWORD is required")?,
                    kafka_tls(config)?,
                )
                .map_err(|error| format!("invalid Kafka SASL config: {error}"))?,
            ),
        };

        let mut builder = Producer::builder()
            .bootstrap_servers(config.bootstrap_servers.clone())
            .client_id(&config.client_id)
            .acks(Acks::All)
            .compression(Compression::Zstd)
            .idempotent(true)
            .max_in_flight(config.max_in_flight)
            .batch_size(config.producer_batch_bytes)
            .linger(Duration::from_millis(config.producer_linger_ms))
            .request_timeout(Duration::from_millis(config.request_timeout_ms))
            .delivery_timeout(Duration::from_millis(config.delivery_timeout_ms))
            .retries(config.producer_retries)
            // Metadata refresh remains bounded so newly provisioned per-Zone
            // topics are discovered without a reconnect storm.
            .metadata_max_age(Duration::from_millis(config.metadata_max_age_ms));
        if let Some(auth) = auth.clone() {
            builder = builder.auth(auth);
        }
        let producer = builder
            .build()
            .await
            .map_err(|error| format!("initialize Kafka producer failed: {error}"))?;
        Ok(Arc::new(Self {
            producer: Arc::new(producer),
            bootstrap_servers: config.bootstrap_servers.clone(),
            auth,
            topic_prefix: config.topic_prefix.trim_end_matches('.').to_string(),
            client_id: config.client_id.clone(),
            request_timeout: Duration::from_millis(config.request_timeout_ms),
            metadata_max_age: Duration::from_millis(config.metadata_max_age_ms),
            consumer_max_poll_records: config.consumer_max_poll_records,
            consumer_session_timeout: Duration::from_millis(config.consumer_session_timeout_ms),
            consumer_heartbeat_interval: Duration::from_millis(
                config.consumer_heartbeat_interval_ms,
            ),
            publish_attempts: config.publish_attempts,
            publish_retry_delay: Duration::from_millis(config.publish_retry_delay_ms),
        }))
    }

    pub async fn consumer(&self, group_id: &str, topic: &str) -> Result<Arc<Consumer>, String> {
        let mut builder = Consumer::builder()
            .bootstrap_servers(self.bootstrap_servers.clone())
            .group_id(group_id)
            .client_id(&self.client_id)
            .auto_offset_reset(AutoOffsetReset::Earliest)
            .enable_auto_commit(false)
            .max_poll_records(self.consumer_max_poll_records)
            .request_timeout(self.request_timeout)
            .session_timeout(self.consumer_session_timeout)
            .heartbeat_interval(self.consumer_heartbeat_interval)
            .metadata_max_age(self.metadata_max_age);
        if let Some(auth) = self.auth.clone() {
            builder = builder.auth(auth);
        }
        let consumer = Arc::new(
            builder
                .build()
                .await
                .map_err(|error| format!("initialize Kafka consumer failed: {error}"))?,
        );
        consumer
            .subscribe(&[topic])
            .await
            .map_err(|error| format!("subscribe {topic} failed: {error}"))?;
        Ok(consumer)
    }

    pub async fn publish_message<M: Message>(
        &self,
        topic: &str,
        key: &[u8],
        message: &M,
    ) -> Result<(), String> {
        let started_at = Instant::now();
        let mut payload = Vec::with_capacity(message.encoded_len());
        if let Err(error) = message.encode(&mut payload) {
            crate::observability::metrics::MetricsManager::record_kafka_operation(
                "publish",
                "failed",
                started_at.elapsed(),
            );
            return Err(format!("encode Kafka Protobuf failed: {error}"));
        }

        // [COMMENT]: Retry loop ngắn hỗ trợ nạp metadata khi tin nhắn đầu tiên gửi tới topic mới vừa khởi tạo.
        let mut attempts = 0u32;
        loop {
            match self.producer.send(topic, Some(key), &payload).await {
                Ok(_) => {
                    crate::observability::metrics::MetricsManager::record_kafka_operation(
                        "publish",
                        "succeeded",
                        started_at.elapsed(),
                    );
                    return Ok(());
                }
                Err(error) => {
                    attempts += 1;
                    if attempts >= self.publish_attempts {
                        crate::observability::metrics::MetricsManager::record_kafka_operation(
                            "publish",
                            "failed",
                            started_at.elapsed(),
                        );
                        return Err(format!("Kafka publish to {topic} failed: {error}"));
                    }
                    tokio::time::sleep(self.publish_retry_delay).await;
                }
            }
        }
    }

    pub async fn commit(
        &self,
        consumer: &Consumer,
        topic: &str,
        partition: i32,
        next_offset: i64,
    ) -> Result<(), String> {
        let started_at = Instant::now();
        let mut offsets = AHashMap::new();
        offsets.insert(
            TopicPartition::new(topic, partition),
            OffsetAndMetadata::with_metadata(next_offset, "aurora-db-transaction-committed"),
        );
        let result = consumer
            .commit_with_metadata(offsets)
            .await
            .map_err(|error| format!("Kafka offset commit failed: {error}"));
        crate::observability::metrics::MetricsManager::record_kafka_operation(
            "commit",
            if result.is_ok() {
                "succeeded"
            } else {
                "failed"
            },
            started_at.elapsed(),
        );
        result
    }

    pub fn zone_command_topic(&self, zone_id: &str) -> String {
        format!("{}.jobs.commands.zone.{}.v1", self.topic_prefix, zone_id)
    }
    pub fn result_topic(&self) -> String {
        format!("{}.jobs.results.v1", self.topic_prefix)
    }
    pub fn metadata_topic(&self, zone_id: &str) -> String {
        format!("{}.zone.metadata.{}.v1", self.topic_prefix, zone_id)
    }
    pub fn metadata_query_topic(&self) -> String {
        format!("{}.zone.metadata.queries.v1", self.topic_prefix)
    }
    pub fn zone_report_topic(&self) -> String {
        format!("{}.zone.reports.v1", self.topic_prefix)
    }
    pub fn storage_sizes_topic(&self) -> String {
        format!("{}.storage.sizes.v1", self.topic_prefix)
    }
    pub fn dead_letter_topic(&self) -> String {
        format!("{}.jobs.dlq.v1", self.topic_prefix)
    }
}

fn kafka_tls(config: &KafkaConfig) -> Result<TlsConfig, String> {
    let tls_config = config
        .tls
        .as_ref()
        .ok_or("Kafka TLS protocol requires an explicit TLS configuration")?;
    let mut tls = TlsConfig::new();
    match tls_config.trust_source {
        TlsTrustSource::System => {
            tls = tls.with_native_roots();
        }
        TlsTrustSource::File => {
            let ca_path = tls_config
                .ca_cert
                .as_ref()
                .ok_or("Kafka file trust requires KAFKA_TLS_CA_CERT")?;
            tls = tls.with_ca_cert(ca_path.to_string_lossy());
        }
    }
    if let Some(server_name) = &config.tls_server_name {
        tls = tls.with_sni_hostname(server_name);
    }
    match (&tls_config.client_cert, &tls_config.client_key) {
        (Some(cert), Some(key)) => {
            tls = tls.with_client_cert(cert.to_string_lossy(), key.to_string_lossy());
        }
        (None, None) => {}
        _ => {
            return Err(
                "KAFKA_TLS_CLIENT_CERT and KAFKA_TLS_CLIENT_KEY must be configured together"
                    .to_owned(),
            )
        }
    }
    Ok(tls)
}
