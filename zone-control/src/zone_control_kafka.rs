use ahash::AHashMap;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{AutoOffsetReset, Consumer, ConsumerRebalanceListener, TopicPartition};
use krafka::producer::{Acks, Producer};
use krafka::protocol::Compression;
use prost::Message;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;

use crate::transfer_ticket::config::Config;

pub(crate) struct KafkaAssignmentFence {
    epoch: AtomicU64,
}

impl KafkaAssignmentFence {
    pub(crate) fn epoch(&self) -> u64 {
        self.epoch.load(Ordering::Acquire)
    }
}

impl Default for KafkaAssignmentFence {
    fn default() -> Self {
        Self {
            epoch: AtomicU64::new(0),
        }
    }
}

impl ConsumerRebalanceListener for KafkaAssignmentFence {
    async fn on_partitions_assigned(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_revoked(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_lost(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }
}

pub(crate) struct ControlKafka {
    producer: Arc<Producer>,
    bootstrap_servers: String,
    auth: Option<AuthConfig>,
    topic_prefix: String,
}

impl ControlKafka {
    pub(crate) async fn connect(config: &Config) -> Result<Arc<Self>, String> {
        let auth = match config.kafka_security_protocol.as_str() {
            "plaintext" => None,
            "ssl" => {
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = &config.kafka_ca_cert {
                    tls = tls.with_ca_cert(ca_path);
                }
                Some(AuthConfig::ssl(tls))
            }
            "sasl_plaintext" => {
                let username = config
                    .kafka_username
                    .as_deref()
                    .ok_or("KAFKA_USERNAME is required for sasl_plaintext")?;
                let password = config
                    .kafka_password
                    .as_deref()
                    .ok_or("KAFKA_PASSWORD is required for sasl_plaintext")?;
                Some(
                    AuthConfig::sasl_plain(username, password)
                        .map_err(|error| format!("invalid Kafka SASL credentials: {error}"))?,
                )
            }
            "sasl_plain_ssl" => {
                let username = config
                    .kafka_username
                    .as_deref()
                    .ok_or("KAFKA_USERNAME is required for sasl_plain_ssl")?;
                let password = config
                    .kafka_password
                    .as_deref()
                    .ok_or("KAFKA_PASSWORD is required for sasl_plain_ssl")?;
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = &config.kafka_ca_cert {
                    tls = tls.with_ca_cert(ca_path);
                }
                Some(
                    AuthConfig::sasl_plain_ssl(username, password, tls)
                        .map_err(|error| format!("invalid Kafka SASL credentials: {error}"))?,
                )
            }
            value => return Err(format!("unsupported KAFKA_SECURITY_PROTOCOL: {value}")),
        };
        let mut builder = Producer::builder()
            .bootstrap_servers(config.kafka_bootstrap_servers.clone())
            .client_id(format!("aurora-zone-control-{}", config.zone_id))
            .acks(Acks::All)
            .compression(Compression::Zstd)
            .idempotent(true)
            .max_in_flight(5)
            .batch_size(65_536)
            .linger(Duration::from_millis(5))
            .request_timeout(Duration::from_secs(10))
            .delivery_timeout(Duration::from_secs(60))
            .retries(10);
        if let Some(auth_value) = auth.clone() {
            builder = builder.auth(auth_value);
        }
        let producer = builder
            .build()
            .await
            .map_err(|error| format!("initialize Zone Control Kafka producer: {error}"))?;
        Ok(Arc::new(Self {
            producer: Arc::new(producer),
            bootstrap_servers: config.kafka_bootstrap_servers.clone(),
            auth,
            topic_prefix: config.kafka_topic_prefix.trim_end_matches('.').to_string(),
        }))
    }

    pub(crate) async fn consumer(
        &self,
        group_id: String,
        topic: &str,
        max_poll_records: i32,
    ) -> Result<(Arc<Consumer>, Arc<KafkaAssignmentFence>), String> {
        let fence = Arc::new(KafkaAssignmentFence::default());
        let mut builder = Consumer::builder()
            .bootstrap_servers(self.bootstrap_servers.clone())
            .group_id(group_id)
            .client_id("aurora-zone-control-internal")
            .rebalance_listener(fence.clone())
            .auto_offset_reset(AutoOffsetReset::Earliest)
            .enable_auto_commit(false)
            .max_poll_records(max_poll_records)
            .request_timeout(Duration::from_secs(10));
        if let Some(auth_value) = self.auth.clone() {
            builder = builder.auth(auth_value);
        }
        let consumer = Arc::new(
            builder
                .build()
                .await
                .map_err(|error| format!("initialize Zone Control Kafka consumer: {error}"))?,
        );
        consumer
            .subscribe(&[topic])
            .await
            .map_err(|error| format!("subscribe Kafka topic {topic}: {error}"))?;
        Ok((consumer, fence))
    }

    pub(crate) async fn commit(
        &self,
        consumer: &Consumer,
        fence: &KafkaAssignmentFence,
        epoch: u64,
        topic: &str,
        partition: i32,
        offset: i64,
    ) -> Result<(), String> {
        if fence.epoch() != epoch {
            return Err("Kafka assignment changed before Zone Control commit".to_string());
        }
        let mut offsets = AHashMap::new();
        offsets.insert(
            TopicPartition::new(topic, partition),
            krafka::consumer::OffsetAndMetadata::with_metadata(
                offset.saturating_add(1),
                "aurora-zone-control-projection-applied",
            ),
        );
        consumer
            .commit_with_metadata(offsets)
            .await
            .map_err(|error| format!("commit Zone Control Kafka offset: {error}"))?;
        if fence.epoch() != epoch {
            return Err("Kafka assignment changed after Zone Control commit".to_string());
        }
        Ok(())
    }

    pub(crate) async fn publish(
        &self,
        topic: &str,
        key: &[u8],
        payload: &[u8],
    ) -> Result<(), String> {
        self.producer
            .send(topic, Some(key), payload)
            .await
            .map(|_| ())
            .map_err(|error| format!("publish Kafka topic {topic}: {error}"))
    }

    pub(crate) async fn publish_proto<M: Message>(
        &self,
        topic: &str,
        key: &[u8],
        value: &M,
    ) -> Result<(), String> {
        let mut payload = Vec::with_capacity(value.encoded_len());
        value
            .encode(&mut payload)
            .map_err(|error| format!("encode Kafka protobuf: {error}"))?;
        self.publish(topic, key, &payload).await
    }

    pub(crate) fn metadata_topic(&self, zone_id: &str) -> String {
        format!("{}.zone.metadata.{}.v1", self.topic_prefix, zone_id)
    }

    pub(crate) fn metadata_query_topic(&self) -> String {
        format!("{}.zone.metadata.queries.v1", self.topic_prefix)
    }

    pub(crate) fn dead_letter_topic(&self) -> String {
        format!("{}.jobs.dlq.v1", self.topic_prefix)
    }

    pub(crate) fn zone_report_topic(&self) -> String {
        format!("{}.zone.reports.v1", self.topic_prefix)
    }

    pub(crate) fn storage_sizes_topic(&self) -> String {
        format!("{}.storage.sizes.v1", self.topic_prefix)
    }

    pub(crate) fn storage_commercial_admission_topic(&self, zone_id: &str) -> String {
        format!(
            "{}.storage.commercial.admission.{}.v1",
            self.topic_prefix, zone_id
        )
    }
}
