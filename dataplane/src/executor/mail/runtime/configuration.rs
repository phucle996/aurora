use crate::config::Config;
use crate::executor::mail::runtime_proto::{
    KafkaStreamPayloadV1, MailConsumerUpsertV1, MailStreamSourceV1, MailStreamType,
    MailTemplateVersionPublishedV1, NatsJetStreamPayloadV1, RabbitMqPayloadV1,
    RedisStreamPayloadV1,
};
use crate::infra::zone_kv::{ConsumerConfigHead, TemplateConfigHead, ZoneKvStore};
use crate::observability::logger::Logger;
use arc_swap::ArcSwap;
use moka::future::Cache;
use opentelemetry::metrics::{Counter, Gauge, Histogram};
use opentelemetry::{global, KeyValue};
use prost::Message;
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant as StdInstant};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

const CONFIG_CONSUMER_HEAD_PREFIX: &str = "mail.consumer.head.";
const CONFIG_CONSUMER_SNAPSHOT_PREFIX: &str = "mail.consumer.snapshot.";
const CONFIG_TEMPLATE_HEAD_PREFIX: &str = "mail.template.head.";
const CONFIG_TEMPLATE_SNAPSHOT_PREFIX: &str = "mail.template.snapshot.";

static CONFIG_APPLY: OnceLock<Counter<u64>> = OnceLock::new();
static CONFIG_ERROR: OnceLock<Counter<u64>> = OnceLock::new();
static TEMPLATE_CACHE_ACCESS: OnceLock<Counter<u64>> = OnceLock::new();
static CONFIG_REGISTRY_SIZE: OnceLock<Gauge<u64>> = OnceLock::new();
static TEMPLATE_CACHE_BYTES: OnceLock<Gauge<u64>> = OnceLock::new();
static CONFIG_SCAN_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static CONFIG_SCAN_ITEMS: OnceLock<Histogram<u64>> = OnceLock::new();

fn config_apply_metric() -> &'static Counter<u64> {
    CONFIG_APPLY.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("mail_configuration_apply_total")
            .with_description("Phase-5 COW configuration outcomes")
            .init()
    })
}

fn config_error_metric() -> &'static Counter<u64> {
    CONFIG_ERROR.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("mail_configuration_error_total")
            .with_description("Phase-5 bounded configuration validation/load failures")
            .init()
    })
}

fn template_cache_access_metric() -> &'static Counter<u64> {
    TEMPLATE_CACHE_ACCESS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("mail_template_l1_access_total")
            .with_description("Immutable mail template L1 hit/miss count")
            .init()
    })
}

fn config_registry_size_metric() -> &'static Gauge<u64> {
    CONFIG_REGISTRY_SIZE.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_configuration_l1_entries")
            .with_description("Phase-5 L1 entries split by active/tombstone state")
            .init()
    })
}

fn template_cache_bytes_metric() -> &'static Gauge<u64> {
    TEMPLATE_CACHE_BYTES.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_template_l1_weighted_bytes")
            .with_description("Approximate byte weight retained by immutable template L1")
            .init()
    })
}

fn config_scan_duration_metric() -> &'static Histogram<f64> {
    CONFIG_SCAN_DURATION.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_histogram("mail_configuration_scan_duration_seconds")
            .with_description("Duration of one bounded Phase-5 NATS KV key slice")
            .init()
    })
}

fn config_scan_items_metric() -> &'static Histogram<u64> {
    CONFIG_SCAN_ITEMS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_histogram("mail_configuration_scan_items")
            .with_description("Consumer heads observed in one bounded NATS KV slice")
            .init()
    })
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum RuntimeDesiredState {
    Paused,
    Enabled,
}

#[derive(Clone, Debug)]
pub struct RuntimeStreamSource {
    pub stream_type: MailStreamType,
    pub payload_schema_version: u32,
    pub broker_resource_id: [u8; 16],
    /// [COMMENT]: Adapter protobuf immutable; chỉ adapter đúng type/version mới được decode bytes này.
    pub payload: Vec<u8>,
}

#[allow(dead_code)] // [COMMENT]: Phase 7 renderer consumes the immutable template fields.
#[derive(Clone, Debug)]
pub struct RuntimeTemplateSnapshot {
    pub template_id: String,
    pub template_revision: u64,
    pub template_version: u64,
    pub content_sha256: [u8; 32],
    pub subject_template: String,
    pub html_template: String,
    /// [COMMENT]: Placeholder offsets được compile một lần lúc L1 miss; hot path không scan lại template cho từng mail.
    pub subject_tokens: Vec<RuntimeTemplateToken>,
    pub html_tokens: Vec<RuntimeTemplateToken>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RuntimeTemplateToken {
    pub start: usize,
    pub end: usize,
    pub key: String,
}

#[allow(dead_code)] // [COMMENT]: Phase 6/7 consume the prepared immutable runtime configuration.
#[derive(Clone, Debug)]
pub struct RuntimeConsumerConfiguration {
    pub consumer_id: String,
    pub config_version: u64,
    pub config_sha256: [u8; 32],
    pub stream: RuntimeStreamSource,
    // [COMMENT]: Consumer chỉ pin identity/version. Template content được lazy-load đúng lúc xử lý message.
    pub template_id: String,
    pub template_version: u64,
    pub sender_profile_id: String,
    pub sender_version: u64,
    pub desired_state: RuntimeDesiredState,
    pub parallelism: u32,
}

#[derive(Clone, Debug)]
pub enum RuntimeConsumerEntry {
    Active(Arc<RuntimeConsumerConfiguration>),
    Tombstone { config_version: u64 },
}

impl RuntimeConsumerEntry {
    pub fn config_version(&self) -> u64 {
        match self {
            Self::Active(configuration) => configuration.config_version,
            Self::Tombstone { config_version } => *config_version,
        }
    }
}

#[derive(Clone, Debug, Hash, PartialEq, Eq)]
struct TemplateCacheKey {
    template_id: String,
    template_version: u64,
}

#[derive(Clone, Debug)]
struct RegistryRecord {
    config_version: u64,
    state: String,
    config_sha256: Option<[u8; 32]>,
}

#[derive(Clone, Debug)]
enum LoadedConsumerObservation {
    Active(Arc<RuntimeConsumerConfiguration>),
    Tombstone {
        consumer_id: String,
        config_version: u64,
    },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum CowApplyOutcome {
    Applied,
    Duplicate,
    Stale,
}

#[derive(Clone, Debug)]
pub(crate) struct ConfigurationLoadError {
    pub(crate) code: &'static str,
    pub(crate) detail: String,
}

impl ConfigurationLoadError {
    fn new(code: &'static str, detail: impl Into<String>) -> Self {
        Self {
            code,
            detail: detail.into(),
        }
    }
}

/// [COMMENT]: Toàn bộ pod chỉ có một Phase-5 runtime: một scheduler, một COW head và một byte-bounded template cache.
pub struct MailConfigurationRuntime {
    zone_id: String,
    instance_id: String,
    scan_interval: Duration,
    scan_page_size: usize,
    scan_max_pages_per_tick: usize,
    max_consumer_entries: usize,
    max_template_bytes: usize,
    consumers: ArcSwap<HashMap<String, RuntimeConsumerEntry>>,
    templates: Cache<TemplateCacheKey, Arc<RuntimeTemplateSnapshot>>,
    apply_lock: tokio::sync::Mutex<()>,
    scan_cursor: AtomicU64,
    cancel: CancellationToken,
    task: Mutex<Option<JoinHandle<()>>>,
    zone_kv: Option<Arc<ZoneKvStore>>,
}

impl MailConfigurationRuntime {
    pub fn new(config: &Config, zone_kv: Arc<ZoneKvStore>) -> Arc<Self> {
        let max_template_bytes = config.mail_template_l1_max_bytes.min(u32::MAX as u64) as usize;
        let templates = Cache::builder()
            // [COMMENT]: Weight theo content bytes để một template lớn không bypass giới hạn chỉ bằng entry count.
            .weigher(
                |_key: &TemplateCacheKey, template: &Arc<RuntimeTemplateSnapshot>| {
                    template
                        .subject_template
                        .len()
                        .saturating_add(template.html_template.len())
                        .saturating_add(template.template_id.len())
                        .saturating_add(
                            template
                                .subject_tokens
                                .len()
                                .saturating_add(template.html_tokens.len())
                                .saturating_mul(64),
                        )
                        .saturating_add(128)
                        .min(u32::MAX as usize) as u32
                },
            )
            .max_capacity(max_template_bytes as u64)
            .time_to_live(Duration::from_secs(config.mail_template_l1_ttl_seconds))
            .build();

        Arc::new(Self {
            zone_id: config.zone_id.clone(),
            instance_id: std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id())),
            scan_interval: Duration::from_secs(config.mail_config_scan_interval_seconds),
            scan_page_size: config.mail_config_scan_page_size,
            scan_max_pages_per_tick: config.mail_config_scan_max_pages_per_tick,
            max_consumer_entries: config.mail_consumer_l1_max_entries,
            // [COMMENT]: Cache có tổng budget riêng; một immutable template vẫn phải nằm dưới per-message hard cap.
            max_template_bytes: config.mail_max_message_bytes,
            consumers: ArcSwap::from_pointee(HashMap::new()),
            templates,
            apply_lock: tokio::sync::Mutex::new(()),
            scan_cursor: AtomicU64::new(0),
            cancel: CancellationToken::new(),
            task: Mutex::new(None),
            zone_kv: Some(zone_kv),
        })
    }

    pub fn start(self: &Arc<Self>, zone_kv: Arc<ZoneKvStore>) {
        let mut task = self.task.lock().expect("mail config task mutex poisoned");
        if task.is_some() {
            return;
        }

        let runtime = self.clone();
        *task = Some(tokio::spawn(async move {
            runtime.run(zone_kv).await;
        }));
    }

    pub fn zone_kv(&self) -> Option<Arc<ZoneKvStore>> {
        self.zone_kv.clone()
    }

    pub async fn shutdown(&self) {
        self.cancel.cancel();
        let handle = self
            .task
            .lock()
            .expect("mail config task mutex poisoned")
            .take();
        if let Some(handle) = handle {
            let _ = handle.await;
        }
    }

    /// [COMMENT]: Phase 6 nhận nguyên generation Arc; reader cũ không bị mutation khi config mới swap.
    #[allow(dead_code)] // [COMMENT]: Phase 6 supervisor consumes a stable generation through this API.
    pub fn snapshot(&self) -> Arc<HashMap<String, RuntimeConsumerEntry>> {
        self.consumers.load_full()
    }

    #[allow(dead_code)] // [COMMENT]: Public read API reserved for the Phase 6 supervisor.
    pub fn active_consumer(&self, consumer_id: &str) -> Option<Arc<RuntimeConsumerConfiguration>> {
        match self.consumers.load().get(consumer_id) {
            Some(RuntimeConsumerEntry::Active(configuration)) => Some(configuration.clone()),
            _ => None,
        }
    }

    /// [COMMENT]: Execution path gọi API này khi có message. Reconciler chỉ hydrate binding nhỏ,
    /// còn template immutable được singleflight qua Moka rồi đọc Zone KV đúng pinned version khi L1 miss.
    #[allow(dead_code)] // [COMMENT]: Phase 6/7 mail executor sẽ gọi khi bắt đầu xử lý một message.
    pub(crate) async fn load_template_for_consumer(
        &self,
        zone_kv: &Arc<ZoneKvStore>,
        configuration: &RuntimeConsumerConfiguration,
    ) -> Result<Arc<RuntimeTemplateSnapshot>, ConfigurationLoadError> {
        let template_key = TemplateCacheKey {
            template_id: configuration.template_id.clone(),
            template_version: configuration.template_version,
        };
        if let Some(template) = self.templates.get(&template_key).await {
            template_cache_access_metric().add(
                1,
                &[
                    KeyValue::new("zone_id", self.zone_id.clone()),
                    KeyValue::new("outcome", "hit"),
                ],
            );
            return Ok(template);
        }

        template_cache_access_metric().add(
            1,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("outcome", "miss"),
            ],
        );
        let head_key = format!("{CONFIG_TEMPLATE_HEAD_PREFIX}{}", configuration.template_id);
        let head_bytes = zone_kv
            .config_get(head_key)
            .await
            .map_err(|_| {
                ConfigurationLoadError::new(
                    "MAIL_TEMPLATE_HEAD_READ_FAILED",
                    "template head read failed",
                )
            })?
            .ok_or_else(|| {
                ConfigurationLoadError::new(
                    "MAIL_TEMPLATE_NOT_ACTIVE",
                    "pinned template head is missing",
                )
            })?;
        let head: TemplateConfigHead = serde_json::from_slice(&head_bytes).map_err(|_| {
            ConfigurationLoadError::new("MAIL_TEMPLATE_HEAD_INVALID", "template head is invalid")
        })?;
        if head.tombstoned {
            return Err(ConfigurationLoadError::new(
                "MAIL_TEMPLATE_NOT_ACTIVE",
                "pinned template is missing or tombstoned",
            ));
        }
        let current_version = head.current_version;
        if current_version < configuration.template_version {
            return Err(ConfigurationLoadError::new(
                "MAIL_TEMPLATE_HEAD_BEHIND",
                "template head is behind consumer pinned version",
            ));
        }
        let expected_current_hash = if current_version == configuration.template_version {
            Some(decode_hex_sha256(&head.content_sha256).ok_or_else(|| {
                ConfigurationLoadError::new(
                    "MAIL_TEMPLATE_HEAD_HASH_INVALID",
                    "current template head SHA-256 is invalid",
                )
            })?)
        } else {
            None
        };

        let zone_kv = zone_kv.clone();
        let load_key = template_key.clone();
        let max_template_bytes = self.max_template_bytes;
        self.templates
            .try_get_with(template_key, async move {
                load_immutable_template(
                    zone_kv,
                    load_key,
                    expected_current_hash,
                    max_template_bytes,
                )
                .await
            })
            .await
            .map_err(|error: Arc<ConfigurationLoadError>| (*error).clone())
    }

    async fn run(self: Arc<Self>, zone_kv: Arc<ZoneKvStore>) {
        // [COMMENT]: NATS KV watch/scan là control-plane signal; jitter giữ các replica lệch pha.
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        self.instance_id.hash(&mut hasher);
        self.zone_id.hash(&mut hasher);
        "mail-phase-5".hash(&mut hasher);
        let initial_jitter = Duration::from_millis(hasher.finish() % 5_000);
        tokio::select! {
            _ = self.cancel.cancelled() => return,
            _ = tokio::time::sleep(initial_jitter) => {}
        }

        loop {
            if let Err(error) = self.reconcile_registry(&zone_kv).await {
                self.record_load_error(&error);
            }
            tokio::select! {
                _ = self.cancel.cancelled() => return,
                _ = tokio::time::sleep(self.scan_interval) => {}
            }
        }
    }

    async fn reconcile_registry(
        &self,
        zone_kv: &Arc<ZoneKvStore>,
    ) -> Result<usize, ConfigurationLoadError> {
        let started = StdInstant::now();
        let cursor = self.scan_cursor.load(Ordering::Relaxed) as usize;
        let budget = self
            .scan_page_size
            .saturating_mul(self.scan_max_pages_per_tick);
        let (keys, has_more) = zone_kv
            .config_keys_page(cursor, budget)
            .await
            .map_err(|_| {
                ConfigurationLoadError::new("MAIL_CONFIG_KV_SCAN_FAILED", "Zone KV key scan failed")
            })?;
        let mut processed = 0_usize;
        for key in &keys {
            if !key.starts_with(CONFIG_CONSUMER_HEAD_PREFIX) {
                continue;
            }
            processed += 1;
            let consumer_id = key.trim_start_matches(CONFIG_CONSUMER_HEAD_PREFIX);
            let normalized_id = match uuid::Uuid::parse_str(consumer_id) {
                Ok(id) => id.to_string(),
                Err(_) => {
                    self.record_load_error(&ConfigurationLoadError::new(
                        "MAIL_CONFIG_CONSUMER_ID_INVALID",
                        "Zone KV contains invalid consumer UUID",
                    ));
                    continue;
                }
            };
            let Some(bytes) = zone_kv.config_get(key.clone()).await.map_err(|_| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_HEAD_READ_FAILED",
                    "consumer head read failed",
                )
            })?
            else {
                continue;
            };
            let head: ConsumerConfigHead = serde_json::from_slice(&bytes).map_err(|_| {
                ConfigurationLoadError::new("MAIL_CONFIG_HEAD_INVALID", "consumer head is invalid")
            })?;
            let record = RegistryRecord {
                config_version: head.version,
                state: if head.tombstoned {
                    "DELETED".to_string()
                } else {
                    head.desired_state.clone()
                },
                config_sha256: if head.tombstoned {
                    None
                } else {
                    decode_hex_sha256(&head.config_sha256)
                },
            };
            if self.registry_record_matches_l1(&normalized_id, &record) {
                continue;
            }
            match self
                .load_consumer_observation(zone_kv, &normalized_id, record.config_version)
                .await
            {
                Ok(observation) => match self.apply_observation(observation).await {
                    Ok(outcome) => self.record_apply(outcome),
                    Err(error) => self.record_load_error(&error),
                },
                Err(error) => self.record_load_error(&error),
            }
        }
        self.scan_cursor.store(
            if has_more {
                cursor.saturating_add(keys.len()) as u64
            } else {
                0
            },
            Ordering::Relaxed,
        );

        config_scan_duration_metric().record(
            started.elapsed().as_secs_f64(),
            &[KeyValue::new("zone_id", self.zone_id.clone())],
        );
        config_scan_items_metric().record(
            processed as u64,
            &[KeyValue::new("zone_id", self.zone_id.clone())],
        );
        self.record_registry_size();
        Ok(processed)
    }

    fn registry_record_matches_l1(&self, consumer_id: &str, record: &RegistryRecord) -> bool {
        match self.consumers.load().get(consumer_id) {
            Some(RuntimeConsumerEntry::Tombstone { config_version }) => {
                *config_version == record.config_version && record.state == "DELETED"
            }
            Some(RuntimeConsumerEntry::Active(configuration)) => {
                let expected_state = match configuration.desired_state {
                    RuntimeDesiredState::Paused => "PAUSED",
                    RuntimeDesiredState::Enabled => "ENABLED",
                };
                configuration.config_version == record.config_version
                    && record.state == expected_state
                    && record
                        .config_sha256
                        .is_some_and(|hash| hash == configuration.config_sha256)
            }
            None => false,
        }
    }

    async fn load_consumer_observation(
        &self,
        zone_kv: &Arc<ZoneKvStore>,
        consumer_id: &str,
        minimum_version: u64,
    ) -> Result<LoadedConsumerObservation, ConfigurationLoadError> {
        let head_key = format!("{CONFIG_CONSUMER_HEAD_PREFIX}{consumer_id}");
        let bytes = zone_kv
            .config_get(head_key)
            .await
            .map_err(|_| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_HEAD_READ_FAILED",
                    "consumer head read failed",
                )
            })?
            .ok_or_else(|| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_HEAD_MISSING",
                    "consumer head is missing; keeping last-known-good",
                )
            })?;
        let head: ConsumerConfigHead = serde_json::from_slice(&bytes).map_err(|_| {
            ConfigurationLoadError::new("MAIL_CONFIG_HEAD_INVALID", "consumer head is invalid")
        })?;
        let config_version = head.version;
        if config_version == 0 {
            return Err(ConfigurationLoadError::new(
                "MAIL_CONFIG_HEAD_INVALID",
                "consumer head version is invalid",
            ));
        }
        if config_version < minimum_version {
            return Err(ConfigurationLoadError::new(
                "MAIL_CONFIG_HEAD_BEHIND_INVALIDATION",
                "consumer head is behind observed registry/invalidation version",
            ));
        }
        if head.tombstoned {
            return Ok(LoadedConsumerObservation::Tombstone {
                consumer_id: consumer_id.to_string(),
                config_version,
            });
        }
        let expected_snapshot_key =
            format!("{CONFIG_CONSUMER_SNAPSHOT_PREFIX}{consumer_id}.v{config_version}");
        let bytes = zone_kv
            .config_get(expected_snapshot_key)
            .await
            .map_err(|_| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_SNAPSHOT_READ_FAILED",
                    "consumer immutable snapshot read failed",
                )
            })?
            .ok_or_else(|| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_SNAPSHOT_MISSING",
                    "consumer immutable snapshot is missing; keeping last-known-good",
                )
            })?;
        let event = MailConsumerUpsertV1::decode(bytes.as_ref()).map_err(|_| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_SNAPSHOT_DECODE_FAILED",
                "consumer immutable snapshot protobuf is invalid",
            )
        })?;

        let event_consumer_id = uuid::Uuid::from_slice(&event.consumer_id)
            .map_err(|_| {
                ConfigurationLoadError::new(
                    "MAIL_CONFIG_CONSUMER_ID_INVALID",
                    "consumer snapshot UUID is invalid",
                )
            })?
            .to_string();
        let head_hash = decode_hex_sha256(&head.config_sha256).ok_or_else(|| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_HEAD_HASH_INVALID",
                "consumer head SHA-256 is invalid",
            )
        })?;
        let event_hash: [u8; 32] = event.config_sha256.as_slice().try_into().map_err(|_| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_HASH_INVALID",
                "consumer snapshot SHA-256 length is invalid",
            )
        })?;
        if event_consumer_id != consumer_id
            || event.config_version != config_version
            || event_hash != head_hash
            || canonical_consumer_sha256(&event) != event_hash
        {
            return Err(ConfigurationLoadError::new(
                "MAIL_CONFIG_INTEGRITY_MISMATCH",
                "consumer identity/version/canonical hash mismatch",
            ));
        }

        let metadata = event.metadata.as_ref().ok_or_else(|| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_METADATA_MISSING",
                "consumer event metadata is missing",
            )
        })?;
        let stream = event.stream.as_ref().ok_or_else(|| {
            ConfigurationLoadError::new("MAIL_CONFIG_STREAM_MISSING", "stream source is missing")
        })?;
        self.validate_consumer_contract(metadata.schema_version, &event, stream)?;

        let desired_state = if event.desired_state == 2 {
            RuntimeDesiredState::Enabled
        } else {
            RuntimeDesiredState::Paused
        };
        let broker_resource_id: [u8; 16] = stream
            .broker_resource_id
            .as_slice()
            .try_into()
            .expect("validated broker resource UUID length");
        Ok(LoadedConsumerObservation::Active(Arc::new(
            RuntimeConsumerConfiguration {
                consumer_id: consumer_id.to_string(),
                config_version,
                config_sha256: event_hash,
                stream: RuntimeStreamSource {
                    stream_type: match stream.stream_type {
                        value if value == MailStreamType::Kafka as i32 => MailStreamType::Kafka,
                        value if value == MailStreamType::RedisStream as i32 => {
                            MailStreamType::RedisStream
                        }
                        value if value == MailStreamType::NatsJetstream as i32 => {
                            MailStreamType::NatsJetstream
                        }
                        value if value == MailStreamType::Rabbitmq as i32 => {
                            MailStreamType::Rabbitmq
                        }
                        _ => unreachable!("validated stream type"),
                    },
                    payload_schema_version: stream.payload_schema_version,
                    broker_resource_id,
                    payload: stream.payload.clone(),
                },
                template_id: event.template_id.clone(),
                template_version: event.template_version,
                sender_profile_id: event.sender_profile_id.clone(),
                sender_version: event.sender_version,
                desired_state,
                parallelism: event.parallelism,
            },
        )))
    }

    fn validate_consumer_contract(
        &self,
        schema_version: u32,
        event: &MailConsumerUpsertV1,
        stream: &MailStreamSourceV1,
    ) -> Result<(), ConfigurationLoadError> {
        let metadata_valid = event
            .metadata
            .as_ref()
            .is_some_and(|metadata| metadata.event_id.len() == 16);
        let stream_type = match stream.stream_type {
            value if value == MailStreamType::Kafka as i32 => Some(MailStreamType::Kafka),
            value if value == MailStreamType::RedisStream as i32 => {
                Some(MailStreamType::RedisStream)
            }
            value if value == MailStreamType::NatsJetstream as i32 => {
                Some(MailStreamType::NatsJetstream)
            }
            value if value == MailStreamType::Rabbitmq as i32 => Some(MailStreamType::Rabbitmq),
            _ => None,
        };
        // [COMMENT]: Mỗi branch decode đúng adapter protobuf của chính nó; outer contract không diễn giải field broker.
        let source_valid = match stream_type {
            Some(MailStreamType::Kafka) if stream.payload_schema_version == 1 => {
                KafkaStreamPayloadV1::decode(stream.payload.as_slice())
                    .ok()
                    .is_some_and(|payload| {
                        payload.source_config_envelope.len() <= 16 << 10
                            && (event.desired_state != 2
                                || !payload.source_config_envelope.is_empty())
                            && !payload.topic.is_empty()
                            && payload.topic.len() <= 249
                            && payload.topic.bytes().all(|byte| {
                                byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-')
                            })
                            && !payload.consumer_group.trim().is_empty()
                            && payload.consumer_group.len() <= 255
                            && !payload.consumer_group.chars().any(char::is_control)
                    })
            }
            Some(MailStreamType::RedisStream) if stream.payload_schema_version == 1 => {
                RedisStreamPayloadV1::decode(stream.payload.as_slice())
                    .ok()
                    .is_some_and(|payload| {
                        payload.source_config_envelope.len() <= 16 << 10
                            && (event.desired_state != 2
                                || !payload.source_config_envelope.is_empty())
                            && !payload.stream_key.trim().is_empty()
                            && payload.stream_key.len() <= 512
                            && !payload.stream_key.chars().any(char::is_control)
                            && !payload.consumer_group.trim().is_empty()
                            && payload.consumer_group.len() <= 255
                            && !payload.consumer_group.chars().any(char::is_control)
                    })
            }
            Some(MailStreamType::NatsJetstream) if stream.payload_schema_version == 1 => {
                NatsJetStreamPayloadV1::decode(stream.payload.as_slice())
                    .ok()
                    .is_some_and(|payload| {
                        payload.source_config_envelope.len() <= 16 << 10
                            && (event.desired_state != 2
                                || !payload.source_config_envelope.is_empty())
                            && !payload.stream_name.trim().is_empty()
                            && payload.stream_name.len() <= 255
                            && !payload.stream_name.chars().any(char::is_control)
                            && !payload.durable_name.trim().is_empty()
                            && payload.durable_name.len() <= 255
                            && !payload.durable_name.chars().any(char::is_control)
                    })
            }
            Some(MailStreamType::Rabbitmq) if stream.payload_schema_version == 1 => {
                RabbitMqPayloadV1::decode(stream.payload.as_slice())
                    .ok()
                    .is_some_and(|payload| {
                        payload.source_config_envelope.len() <= 16 << 10
                            && (event.desired_state != 2
                                || !payload.source_config_envelope.is_empty())
                            && !payload.queue_name.trim().is_empty()
                            && payload.queue_name.len() <= 255
                            && !payload.queue_name.chars().any(char::is_control)
                            && !payload.consumer_tag_prefix.trim().is_empty()
                            && payload.consumer_tag_prefix.len() <= 128
                            && !payload.consumer_tag_prefix.chars().any(char::is_control)
                    })
            }
            _ => false,
        };
        if schema_version != 1
            || !metadata_valid
            || stream_type.is_none()
            || stream_type == Some(MailStreamType::Unspecified)
            || stream.payload_schema_version == 0
            || stream.broker_resource_id.len() != 16
            || stream.payload.is_empty()
            || stream.payload.len() > 32 << 10
            || !source_valid
            || event.template_id.trim().is_empty()
            || event.template_id.len() > 256
            || uuid::Uuid::parse_str(&event.template_id).is_err()
            || event.template_version == 0
            || event.sender_profile_id.trim().is_empty()
            || event.sender_profile_id.len() > 256
            || event.sender_profile_id.chars().any(char::is_control)
            || event.sender_version == 0
            || !matches!(event.desired_state, 1 | 2)
            || event.parallelism == 0
            || event.parallelism > 256
        {
            return Err(ConfigurationLoadError::new(
                "MAIL_CONFIG_CONTRACT_INVALID",
                "consumer snapshot violates bounded runtime contract",
            ));
        }
        Ok(())
    }

    async fn apply_observation(
        &self,
        observation: LoadedConsumerObservation,
    ) -> Result<CowApplyOutcome, ConfigurationLoadError> {
        let _guard = self.apply_lock.lock().await;
        let current = self.consumers.load_full();
        let (consumer_id, incoming_version) = match &observation {
            LoadedConsumerObservation::Active(configuration) => (
                configuration.consumer_id.clone(),
                configuration.config_version,
            ),
            LoadedConsumerObservation::Tombstone {
                consumer_id,
                config_version,
            } => (consumer_id.clone(), *config_version),
        };

        if let Some(existing) = current.get(&consumer_id) {
            if incoming_version < existing.config_version() {
                return Ok(CowApplyOutcome::Stale);
            }
            if incoming_version == existing.config_version() {
                let duplicate = match (&observation, existing) {
                    (
                        LoadedConsumerObservation::Active(incoming),
                        RuntimeConsumerEntry::Active(existing),
                    ) => incoming.config_sha256 == existing.config_sha256,
                    (
                        LoadedConsumerObservation::Tombstone { .. },
                        RuntimeConsumerEntry::Tombstone { .. },
                    ) => true,
                    _ => false,
                };
                if duplicate {
                    return Ok(CowApplyOutcome::Duplicate);
                }
                return Err(ConfigurationLoadError::new(
                    "MAIL_CONFIG_L1_VERSION_CONFLICT",
                    "same consumer version has different L1 observation",
                ));
            }
        }

        let mut next = (*current).clone();
        if !next.contains_key(&consumer_id) && next.len() >= self.max_consumer_entries {
            // [COMMENT]: Tombstone local chỉ là fence phụ; Zone KV head vẫn authoritative nên có thể evict một tombstone để ưu tiên active config.
            if let Some(evict_id) = next.iter().find_map(|(id, entry)| {
                matches!(entry, RuntimeConsumerEntry::Tombstone { .. }).then(|| id.clone())
            }) {
                next.remove(&evict_id);
            } else {
                return Err(ConfigurationLoadError::new(
                    "MAIL_CONFIG_L1_CAPACITY_REACHED",
                    "consumer L1 hard capacity reached",
                ));
            }
        }

        match observation {
            LoadedConsumerObservation::Active(configuration) => {
                next.insert(consumer_id, RuntimeConsumerEntry::Active(configuration));
            }
            LoadedConsumerObservation::Tombstone { config_version, .. } => {
                next.insert(
                    consumer_id,
                    RuntimeConsumerEntry::Tombstone { config_version },
                );
            }
        }
        // [COMMENT]: Swap đúng một pointer; mọi reader đang giữ Arc generation cũ tiếp tục an toàn tới khi hoàn thành.
        self.consumers.store(Arc::new(next));
        self.record_registry_size();
        Ok(CowApplyOutcome::Applied)
    }

    fn record_apply(&self, outcome: CowApplyOutcome) {
        let outcome = match outcome {
            CowApplyOutcome::Applied => "applied",
            CowApplyOutcome::Duplicate => "duplicate",
            CowApplyOutcome::Stale => "stale",
        };
        config_apply_metric().add(
            1,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("outcome", outcome),
            ],
        );
    }

    fn record_load_error(&self, error: &ConfigurationLoadError) {
        config_error_metric().add(
            1,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("code", error.code),
            ],
        );
        // [COMMENT]: Diagnostic được kiểm soát trong code; không log payload, template body, topic hay Vault reference.
        let expected_repair_state = matches!(
            error.code,
            "MAIL_CONFIG_HEAD_MISSING"
                | "MAIL_CONFIG_SNAPSHOT_MISSING"
                | "MAIL_TEMPLATE_NOT_ACTIVE"
                | "MAIL_TEMPLATE_HEAD_BEHIND"
                | "MAIL_TEMPLATE_SNAPSHOT_MISSING"
                | "MAIL_CONFIG_KV_UNAVAILABLE"
                | "MAIL_CONFIG_KV_SCAN_FAILED"
        );
        if expected_repair_state {
            Logger::sys_debug("mail.configuration", error.code);
        } else {
            Logger::sys_warn("mail.configuration", &error.detail, error.code);
        }
    }

    fn record_registry_size(&self) {
        let snapshot = self.consumers.load();
        let active = snapshot
            .values()
            .filter(|entry| matches!(entry, RuntimeConsumerEntry::Active(_)))
            .count();
        let tombstone = snapshot.len().saturating_sub(active);
        config_registry_size_metric().record(
            active as u64,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("state", "active"),
            ],
        );
        config_registry_size_metric().record(
            tombstone as u64,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("state", "tombstone"),
            ],
        );
        template_cache_bytes_metric().record(
            self.templates.weighted_size(),
            &[KeyValue::new("zone_id", self.zone_id.clone())],
        );
    }
}

async fn load_immutable_template(
    zone_kv: Arc<ZoneKvStore>,
    key: TemplateCacheKey,
    expected_current_hash: Option<[u8; 32]>,
    max_template_bytes: usize,
) -> Result<Arc<RuntimeTemplateSnapshot>, ConfigurationLoadError> {
    let snapshot_key = format!(
        "{CONFIG_TEMPLATE_SNAPSHOT_PREFIX}{}.v{}",
        key.template_id, key.template_version
    );
    let bytes = zone_kv.config_get(snapshot_key).await.map_err(|_| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_SNAPSHOT_READ_FAILED",
            "template immutable snapshot read failed",
        )
    })?;
    let bytes = bytes.ok_or_else(|| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_SNAPSHOT_MISSING",
            "pinned template snapshot is missing; keeping last-known-good",
        )
    })?;
    let event = MailTemplateVersionPublishedV1::decode(bytes.as_ref()).map_err(|_| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_SNAPSHOT_DECODE_FAILED",
            "template immutable snapshot protobuf is invalid",
        )
    })?;
    let metadata_valid = event
        .metadata
        .as_ref()
        .is_some_and(|metadata| metadata.schema_version == 1 && metadata.event_id.len() == 16);
    let event_hash: [u8; 32] = event.content_sha256.as_slice().try_into().map_err(|_| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_HASH_INVALID",
            "template snapshot SHA-256 length is invalid",
        )
    })?;
    let content_bytes = event
        .subject_template
        .len()
        .saturating_add(event.html_template.len());
    if !metadata_valid
        || uuid::Uuid::parse_str(&event.template_id).is_err()
        || event.template_id != key.template_id
        || event.template_version != key.template_version
        || event.template_revision == 0
        || event.subject_template.trim().is_empty()
        || event.subject_template.contains(['\r', '\n'])
        || event.html_template.trim().is_empty()
        || content_bytes > max_template_bytes
        || canonical_template_sha256(&event.subject_template, &event.html_template) != event_hash
        || expected_current_hash.is_some_and(|hash| hash != event_hash)
    {
        return Err(ConfigurationLoadError::new(
            "MAIL_TEMPLATE_INTEGRITY_MISMATCH",
            "template identity/version/canonical hash mismatch",
        ));
    }
    let subject_tokens = compile_template_tokens(&event.subject_template).map_err(|_| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_SYNTAX_INVALID",
            "template subject placeholder syntax is invalid",
        )
    })?;
    let html_tokens = compile_template_tokens(&event.html_template).map_err(|_| {
        ConfigurationLoadError::new(
            "MAIL_TEMPLATE_SYNTAX_INVALID",
            "template HTML placeholder syntax is invalid",
        )
    })?;
    Ok(Arc::new(RuntimeTemplateSnapshot {
        template_id: event.template_id,
        template_revision: event.template_revision,
        template_version: event.template_version,
        content_sha256: event_hash,
        subject_template: event.subject_template,
        html_template: event.html_template,
        subject_tokens,
        html_tokens,
    }))
}

pub(crate) fn compile_template_tokens(
    template: &str,
) -> Result<Vec<RuntimeTemplateToken>, &'static str> {
    let mut tokens = Vec::new();
    let mut cursor = 0_usize;
    while let Some(relative_open) = template[cursor..].find("{{") {
        let start = cursor + relative_open;
        if template[cursor..start].contains("}}") {
            return Err("MAIL_TEMPLATE_SYNTAX_INVALID");
        }
        let token_start = start + 2;
        let relative_close = template[token_start..]
            .find("}}")
            .ok_or("MAIL_TEMPLATE_SYNTAX_INVALID")?;
        let close = token_start + relative_close;
        let key = template[token_start..close].trim();
        if key.contains(['{', '}']) || !valid_template_parameter_key(key) {
            return Err("MAIL_TEMPLATE_SYNTAX_INVALID");
        }
        tokens.push(RuntimeTemplateToken {
            start,
            end: close + 2,
            key: key.to_string(),
        });
        cursor = close + 2;
    }
    if template[cursor..].contains("}}") {
        return Err("MAIL_TEMPLATE_SYNTAX_INVALID");
    }
    Ok(tokens)
}

fn valid_template_parameter_key(key: &str) -> bool {
    if key.is_empty() || key.len() > 128 {
        return false;
    }
    let mut bytes = key.bytes();
    bytes
        .next()
        .is_some_and(|byte| byte.is_ascii_alphabetic() || byte == b'_')
        && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
}

#[cfg(test)]
fn parse_registry_record(raw: &str) -> Result<RegistryRecord, ConfigurationLoadError> {
    let parts = raw.splitn(3, '|').collect::<Vec<_>>();
    if parts.len() != 3 {
        return Err(ConfigurationLoadError::new(
            "MAIL_CONFIG_REGISTRY_RECORD_INVALID",
            "consumer registry record has invalid shape",
        ));
    }
    let config_version = parts[0]
        .parse::<u64>()
        .ok()
        .filter(|version| *version > 0)
        .ok_or_else(|| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_REGISTRY_RECORD_INVALID",
                "consumer registry version is invalid",
            )
        })?;
    if !matches!(parts[1], "PAUSED" | "ENABLED" | "DELETED") {
        return Err(ConfigurationLoadError::new(
            "MAIL_CONFIG_REGISTRY_RECORD_INVALID",
            "consumer registry desired state is invalid",
        ));
    }
    let config_sha256 = if parts[1] == "DELETED" {
        if !parts[2].is_empty() {
            return Err(ConfigurationLoadError::new(
                "MAIL_CONFIG_REGISTRY_RECORD_INVALID",
                "consumer tombstone unexpectedly contains a config hash",
            ));
        }
        None
    } else {
        Some(decode_hex_sha256(parts[2]).ok_or_else(|| {
            ConfigurationLoadError::new(
                "MAIL_CONFIG_REGISTRY_RECORD_INVALID",
                "consumer registry SHA-256 is invalid",
            )
        })?)
    };
    Ok(RegistryRecord {
        config_version,
        state: parts[1].to_string(),
        config_sha256,
    })
}

fn decode_hex_sha256(value: &str) -> Option<[u8; 32]> {
    if value.len() != 64 {
        return None;
    }
    let mut output = [0_u8; 32];
    for (index, byte) in output.iter_mut().enumerate() {
        *byte = u8::from_str_radix(&value[index * 2..index * 2 + 2], 16).ok()?;
    }
    Some(output)
}

/// [COMMENT]: CP hash bỏ metadata/config_sha256 rồi deterministic-marshal protobuf; primitive này tái tạo đúng wire order.
pub(crate) fn canonical_consumer_sha256(event: &MailConsumerUpsertV1) -> [u8; 32] {
    let mut bytes = Vec::new();
    push_bytes_field(&mut bytes, 2, &event.consumer_id);
    push_varint_field(&mut bytes, 3, event.config_version);
    if let Some(stream) = &event.stream {
        let mut nested = Vec::new();
        push_varint_field(&mut nested, 1, stream.stream_type as u64);
        push_varint_field(&mut nested, 2, stream.payload_schema_version as u64);
        push_bytes_field(&mut nested, 3, &stream.broker_resource_id);
        push_bytes_field(&mut nested, 4, &stream.payload);
        push_bytes_field(&mut bytes, 4, &nested);
    }
    push_string_field(&mut bytes, 6, &event.template_id);
    push_varint_field(&mut bytes, 7, event.template_version);
    push_string_field(&mut bytes, 8, &event.sender_profile_id);
    push_varint_field(&mut bytes, 9, event.sender_version);
    push_varint_field(&mut bytes, 10, event.desired_state as u64);
    push_varint_field(&mut bytes, 11, event.parallelism as u64);
    Sha256::digest(bytes).into()
}

pub(crate) fn canonical_template_sha256(subject: &str, html: &str) -> [u8; 32] {
    // [COMMENT]: Go encoding/json escape HTML mặc định; giữ cùng canonical contract giữa CP producer và Rust consumer.
    let subject = go_json_string(subject);
    let html = go_json_string(html);
    Sha256::digest(format!("{{\"subject\":{subject},\"html\":{html}}}").as_bytes()).into()
}

fn go_json_string(value: &str) -> String {
    serde_json::to_string(value)
        .expect("serializing a Rust string cannot fail")
        .replace('&', "\\u0026")
        .replace('<', "\\u003c")
        .replace('>', "\\u003e")
        .replace('\u{2028}', "\\u2028")
        .replace('\u{2029}', "\\u2029")
}

fn push_string_field(output: &mut Vec<u8>, field: u64, value: &str) {
    if !value.is_empty() {
        push_bytes_field(output, field, value.as_bytes());
    }
}

fn push_bytes_field(output: &mut Vec<u8>, field: u64, value: &[u8]) {
    if value.is_empty() {
        return;
    }
    push_varint(output, (field << 3) | 2);
    push_varint(output, value.len() as u64);
    output.extend_from_slice(value);
}

fn push_varint_field(output: &mut Vec<u8>, field: u64, value: u64) {
    if value != 0 {
        push_varint(output, field << 3);
        push_varint(output, value);
    }
}

fn push_varint(output: &mut Vec<u8>, mut value: u64) {
    while value >= 0x80 {
        output.push((value as u8) | 0x80);
        value >>= 7;
    }
    output.push(value as u8);
}

#[cfg(test)]
#[path = "../test/runtime_configuration.rs"]
mod tests;
