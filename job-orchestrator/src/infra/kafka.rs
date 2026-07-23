use ahash::AHashMap;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{AutoOffsetReset, Consumer, OffsetAndMetadata, TopicPartition};
use krafka::producer::{Acks, Producer};
use krafka::protocol::Compression;
use prost::Message;
use std::sync::Arc;
use std::time::Duration;

pub mod transport_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.transport.v1.rs"));
}

pub struct KafkaTransport {
    producer: Arc<Producer>,
    bootstrap_servers: String,
    auth: Option<AuthConfig>,
    topic_prefix: String,
}

impl KafkaTransport {
    pub async fn connect(config: &crate::config::Config) -> Result<Arc<Self>, String> {
        let auth = match config.kafka_security_protocol.as_str() {
            "plaintext" => None,
            "ssl" => {
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = &config.kafka_ca_cert {
                    tls = tls.with_ca_cert(ca_path);
                }
                Some(AuthConfig::ssl(tls))
            }
            "sasl_plaintext" => Some(
                AuthConfig::sasl_plain(
                    config
                        .kafka_username
                        .as_deref()
                        .ok_or("KAFKA_USERNAME is required")?,
                    config
                        .kafka_password
                        .as_deref()
                        .ok_or("KAFKA_PASSWORD is required")?,
                )
                .map_err(|error| format!("invalid Kafka SASL config: {error}"))?,
            ),
            "sasl_plain_ssl" => {
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = &config.kafka_ca_cert {
                    tls = tls.with_ca_cert(ca_path);
                }
                Some(
                    AuthConfig::sasl_plain_ssl(
                        config
                            .kafka_username
                            .as_deref()
                            .ok_or("KAFKA_USERNAME is required")?,
                        config
                            .kafka_password
                            .as_deref()
                            .ok_or("KAFKA_PASSWORD is required")?,
                        tls,
                    )
                    .map_err(|error| format!("invalid Kafka SASL config: {error}"))?,
                )
            }
            value => return Err(format!("unsupported KAFKA_SECURITY_PROTOCOL: {value}")),
        };

        let mut builder = Producer::builder()
            .bootstrap_servers(config.kafka_bootstrap_servers.clone())
            .client_id("aurora-job-orchestrator")
            .acks(Acks::All)
            .compression(Compression::Zstd)
            .idempotent(true)
            .max_in_flight(5)
            .batch_size(65_536)
            .linger(Duration::from_millis(5))
            .request_timeout(Duration::from_secs(10))
            .delivery_timeout(Duration::from_secs(60))
            .retries(10);
        if let Some(auth) = auth.clone() {
            builder = builder.auth(auth);
        }
        let producer = builder
            .build()
            .await
            .map_err(|error| format!("initialize Kafka producer failed: {error}"))?;
        Ok(Arc::new(Self {
            producer: Arc::new(producer),
            bootstrap_servers: config.kafka_bootstrap_servers.clone(),
            auth,
            topic_prefix: config.kafka_topic_prefix.trim_end_matches('.').to_string(),
        }))
    }

    pub async fn consumer(&self, group_id: &str, topic: &str) -> Result<Arc<Consumer>, String> {
        let mut builder = Consumer::builder()
            .bootstrap_servers(self.bootstrap_servers.clone())
            .group_id(group_id)
            .client_id("aurora-job-orchestrator")
            .auto_offset_reset(AutoOffsetReset::Earliest)
            .enable_auto_commit(false)
            .max_poll_records(32)
            .request_timeout(Duration::from_secs(10));
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
        let mut payload = Vec::with_capacity(message.encoded_len());
        message
            .encode(&mut payload)
            .map_err(|error| format!("encode Kafka Protobuf failed: {error}"))?;
        self.producer
            .send(topic, Some(key), &payload)
            .await
            .map(|_| ())
            .map_err(|error| format!("Kafka publish to {topic} failed: {error}"))
    }

    pub async fn commit(
        &self,
        consumer: &Consumer,
        topic: &str,
        partition: i32,
        next_offset: i64,
    ) -> Result<(), String> {
        let mut offsets = AHashMap::new();
        offsets.insert(
            TopicPartition::new(topic, partition),
            OffsetAndMetadata::with_metadata(next_offset, "aurora-db-transaction-committed"),
        );
        consumer
            .commit_with_metadata(offsets)
            .await
            .map_err(|error| format!("Kafka offset commit failed: {error}"))
    }

    pub fn zone_command_topic(&self, zone_id: &str) -> String {
        format!("{}.jobs.commands.zone.{}.v1", self.topic_prefix, zone_id)
    }
    pub fn platform_command_topic(&self) -> String {
        format!("{}.jobs.commands.platform.v1", self.topic_prefix)
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
