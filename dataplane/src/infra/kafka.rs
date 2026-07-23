use ahash::AHashMap;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{
    AutoOffsetReset, Consumer, ConsumerRebalanceListener, OffsetAndMetadata, TopicPartition,
};
use krafka::producer::{Acks, Producer};
use krafka::protocol::Compression;
use prost::Message;
use std::collections::{BTreeSet, HashMap};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::Mutex;

pub mod transport_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.transport.v1.rs"));
}

#[derive(Default)]
pub struct KafkaRebalanceFence {
    epoch: AtomicU64,
}

impl KafkaRebalanceFence {
    pub fn epoch(&self) -> u64 {
        self.epoch.load(Ordering::Acquire)
    }
}

impl ConsumerRebalanceListener for KafkaRebalanceFence {
    async fn on_partitions_assigned(&self, _partitions: &[TopicPartition]) {
        // [COMMENT]: Mỗi assignment round fence toàn bộ completion sinh từ owner cũ.
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_revoked(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }

    async fn on_partitions_lost(&self, _partitions: &[TopicPartition]) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
    }
}

struct PartitionSettlement {
    next_offset: i64,
    terminal_offsets: BTreeSet<i64>,
}

pub struct KafkaSettlement {
    consumer: Arc<Consumer>,
    fence: Arc<KafkaRebalanceFence>,
    partitions: Mutex<HashMap<(u64, String, i32), PartitionSettlement>>,
}

impl KafkaSettlement {
    pub fn new(consumer: Arc<Consumer>, fence: Arc<KafkaRebalanceFence>) -> Arc<Self> {
        Arc::new(Self {
            consumer,
            fence,
            partitions: Mutex::new(HashMap::new()),
        })
    }

    pub async fn register(&self, epoch: u64, topic: &str, partition: i32, offset: i64) {
        let mut partitions = self.partitions.lock().await;
        partitions
            .entry((epoch, topic.to_string(), partition))
            .and_modify(|state| state.next_offset = state.next_offset.min(offset))
            .or_insert_with(|| PartitionSettlement {
                next_offset: offset,
                terminal_offsets: BTreeSet::new(),
            });
    }

    pub async fn settle(
        &self,
        epoch: u64,
        topic: &str,
        partition: i32,
        offset: i64,
    ) -> Result<(), String> {
        // [COMMENT]: Completion của assignment cũ tuyệt đối không được commit offset của owner mới.
        if self.fence.epoch() != epoch {
            return Err(
                "Kafka assignment changed before completion; offset left uncommitted".into(),
            );
        }

        let commit_next = {
            let mut partitions = self.partitions.lock().await;
            let state = partitions
                .entry((epoch, topic.to_string(), partition))
                .or_insert_with(|| PartitionSettlement {
                    next_offset: offset,
                    terminal_offsets: BTreeSet::new(),
                });
            state.terminal_offsets.insert(offset);
            while state.terminal_offsets.remove(&state.next_offset) {
                state.next_offset += 1;
            }
            state.next_offset
        };

        let mut offsets = AHashMap::new();
        offsets.insert(
            TopicPartition::new(topic, partition),
            OffsetAndMetadata::with_metadata(commit_next, "aurora-terminal-durable"),
        );
        self.consumer
            .commit_with_metadata(offsets)
            .await
            .map_err(|error| format!("commit Kafka offset failed: {error}"))
    }
}

#[derive(Clone)]
pub struct KafkaDelivery {
    pub topic: String,
    pub partition: i32,
    pub offset: i64,
    pub assignment_epoch: u64,
    settlement: Arc<KafkaSettlement>,
}

impl std::fmt::Debug for KafkaDelivery {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("KafkaDelivery")
            .field("topic", &self.topic)
            .field("partition", &self.partition)
            .field("offset", &self.offset)
            .field("assignment_epoch", &self.assignment_epoch)
            .finish_non_exhaustive()
    }
}

impl KafkaDelivery {
    pub fn new(
        topic: String,
        partition: i32,
        offset: i64,
        assignment_epoch: u64,
        settlement: Arc<KafkaSettlement>,
    ) -> Self {
        Self {
            topic,
            partition,
            offset,
            assignment_epoch,
            settlement,
        }
    }

    pub async fn settle(&self) -> Result<(), String> {
        self.settlement
            .settle(
                self.assignment_epoch,
                &self.topic,
                self.partition,
                self.offset,
            )
            .await
    }
}

pub struct KafkaTransport {
    producer: Arc<Producer>,
    bootstrap_servers: String,
    auth: Option<AuthConfig>,
    topic_prefix: String,
    observed_job_lag: AtomicU64,
    observed_job_lag_stale: AtomicBool,
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
            .client_id(format!("aurora-dataplane-{}", config.zone_id))
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
            observed_job_lag: AtomicU64::new(0),
            observed_job_lag_stale: AtomicBool::new(true),
        }))
    }

    pub async fn consumer(
        &self,
        group_id: String,
        topic: &str,
        max_poll_records: i32,
    ) -> Result<(Arc<Consumer>, Arc<KafkaRebalanceFence>), String> {
        let fence = Arc::new(KafkaRebalanceFence::default());
        let mut builder = Consumer::builder()
            .bootstrap_servers(self.bootstrap_servers.clone())
            .group_id(group_id)
            .client_id("aurora-dataplane-internal")
            .rebalance_listener(fence.clone())
            .auto_offset_reset(AutoOffsetReset::Earliest)
            .enable_auto_commit(false)
            .max_poll_records(max_poll_records)
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
            .map_err(|error| format!("subscribe Kafka topic {topic} failed: {error}"))?;
        Ok((consumer, fence))
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

    pub fn dead_letter_topic(&self) -> String {
        format!("{}.jobs.dlq.v1", self.topic_prefix)
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

    pub async fn publish(&self, topic: &str, key: &[u8], payload: &[u8]) -> Result<(), String> {
        self.producer
            .send(topic, Some(key), payload)
            .await
            .map(|_| ())
            .map_err(|error| format!("Kafka publish to {topic} failed: {error}"))
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
        self.publish(topic, key, &payload).await
    }

    pub async fn observe_job_lag(&self, consumer: &Consumer) {
        let lag = consumer.lag().await;
        self.observed_job_lag
            .store(lag.lag.values().copied().sum(), Ordering::Release);
        self.observed_job_lag_stale
            .store(!lag.stale_partitions.is_empty(), Ordering::Release);
    }

    pub fn job_lag_snapshot(&self) -> (u64, bool) {
        (
            self.observed_job_lag.load(Ordering::Acquire),
            self.observed_job_lag_stale.load(Ordering::Acquire),
        )
    }
}
