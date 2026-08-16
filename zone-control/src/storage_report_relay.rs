use std::{path::PathBuf, sync::Arc, time::Duration};

use async_nats::jetstream::{
    self,
    consumer::{AckPolicy, PullConsumer},
    message::PublishMessage,
    stream::StorageType,
};
use bytes::Bytes;
use futures_util::StreamExt;
use krafka::{
    auth::{AuthConfig, TlsConfig},
    producer::{Acks, Producer},
    protocol::Compression,
};
use prost::Message;
use serde::Serialize;
use sha2::{Digest, Sha256};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    metering::validate_report, storage_usage_report_proto::StorageUsageReportV1,
    transfer_ticket::config::Config, zone_control_state::ZoneControlState,
};

const OUTBOX_STREAM: &str = "AURORA_ZONE_STORAGE_USAGE_OUTBOX";
const OUTBOX_SUBJECT_PREFIX: &str = "aurora.zone.storage.usage.report";
const OUTBOX_DLQ_STREAM: &str = "AURORA_ZONE_STORAGE_USAGE_DLQ";
const OUTBOX_DLQ_SUBJECT: &str = "aurora.zone.storage.usage.dlq";
const OUTBOX_CONSUMER: &str = "zone-control-storage-report-kafka-v1";
const REPORT_TOPIC_SUFFIX: &str = "storage.usage.reports.v1";
const MAX_REPORT_BYTES: usize = 512 * 1024;

#[derive(Clone)]
struct Settings {
    zone_id: Uuid,
    kafka_bootstrap_servers: String,
    kafka_security_protocol: String,
    kafka_username: Option<String>,
    kafka_password: Option<String>,
    kafka_ca_cert: Option<String>,
    kafka_topic_prefix: String,
}

impl Settings {
    fn from_env(config: &Config) -> Result<Self, String> {
        let zone_id = Uuid::parse_str(&config.zone_id)
            .map_err(|_| "ZONE_ID is invalid for storage report relay".to_string())?;
        if zone_id.is_nil() {
            return Err("ZONE_ID must be non-nil for storage report relay".to_string());
        }
        Ok(Self {
            zone_id,
            kafka_bootstrap_servers: required_env("KAFKA_BOOTSTRAP_SERVERS")?,
            kafka_security_protocol: required_env("KAFKA_SECURITY_PROTOCOL")?.to_ascii_lowercase(),
            kafka_username: optional_env("KAFKA_USERNAME"),
            kafka_password: optional_env("KAFKA_PASSWORD"),
            kafka_ca_cert: optional_env("KAFKA_CA_CERT"),
            kafka_topic_prefix: required_env("KAFKA_TOPIC_PREFIX")?
                .trim_end_matches('.')
                .to_string(),
        })
    }

    fn report_topic(&self) -> String {
        format!("{}.{}", self.kafka_topic_prefix, REPORT_TOPIC_SUFFIX)
    }
}

#[derive(Debug, Serialize)]
struct DeadLetterRecord {
    schema_version: u32,
    source: &'static str,
    reason: &'static str,
    payload_sha256: String,
    zone_id: String,
}

struct KafkaPublisher {
    producer: Arc<Producer>,
    topic: String,
}

impl KafkaPublisher {
    async fn connect(settings: &Settings) -> Result<Self, String> {
        let auth = match settings.kafka_security_protocol.as_str() {
            "plaintext" => None,
            "ssl" => {
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = settings.kafka_ca_cert.as_deref() {
                    tls = tls.with_ca_cert(ca_path);
                }
                Some(AuthConfig::ssl(tls))
            }
            "sasl_plaintext" => {
                let username = settings
                    .kafka_username
                    .as_deref()
                    .ok_or("KAFKA_USERNAME is required for sasl_plaintext")?;
                let password = settings
                    .kafka_password
                    .as_deref()
                    .ok_or("KAFKA_PASSWORD is required for sasl_plaintext")?;
                Some(
                    AuthConfig::sasl_plain(username, password)
                        .map_err(|error| format!("invalid Kafka SASL credentials: {error}"))?,
                )
            }
            "sasl_plain_ssl" => {
                let username = settings
                    .kafka_username
                    .as_deref()
                    .ok_or("KAFKA_USERNAME is required for sasl_plain_ssl")?;
                let password = settings
                    .kafka_password
                    .as_deref()
                    .ok_or("KAFKA_PASSWORD is required for sasl_plain_ssl")?;
                let mut tls = TlsConfig::new().with_native_roots();
                if let Some(ca_path) = settings.kafka_ca_cert.as_deref() {
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
            .bootstrap_servers(settings.kafka_bootstrap_servers.clone())
            .client_id(format!(
                "aurora-zone-control-storage-report-relay-{}",
                settings.zone_id
            ))
            .acks(Acks::All)
            .compression(Compression::Zstd)
            .idempotent(true)
            .max_in_flight(5)
            .batch_size(65_536)
            .linger(Duration::from_millis(5))
            .request_timeout(Duration::from_secs(10))
            .delivery_timeout(Duration::from_secs(60))
            .retries(10);
        if let Some(auth) = auth {
            builder = builder.auth(auth);
        }
        let producer = builder
            .build()
            .await
            .map_err(|error| format!("initialize storage report Kafka producer: {error}"))?;
        Ok(Self {
            producer: Arc::new(producer),
            topic: settings.report_topic(),
        })
    }

    async fn publish(&self, report: &StorageUsageReportV1) -> Result<(), String> {
        let payload = report.encode_to_vec();
        self.producer
            .send(&self.topic, Some(report.zone_id.as_bytes()), &payload)
            .await
            .map(|_| ())
            .map_err(|error| format!("publish StorageUsageReportV1 to Kafka: {error}"))
    }
}

/// Relays already-durable Zone reports to Kafka. This workflow owns no
/// ClickHouse aggregation state and acknowledges NATS only after Kafka accepts
/// the report, so it can restart independently from the metering publisher.
pub async fn run(
    config: Config,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    if shutdown.is_cancelled() {
        return Ok(());
    }
    let settings = Settings::from_env(&config)
        .map_err(|error| format!("ZONE_STORAGE_REPORT_RELAY_CONFIG_INVALID: {error}"))?;
    let client = connect_nats(&config).await?;
    let js = jetstream::new(client);
    let stream = js
        .get_or_create_stream(jetstream::stream::Config {
            name: OUTBOX_STREAM.to_string(),
            subjects: vec![format!("{OUTBOX_SUBJECT_PREFIX}.>")],
            max_bytes: 1_073_741_824,
            max_age: Duration::from_secs(30 * 86_400),
            max_message_size: MAX_REPORT_BYTES as i32,
            storage: StorageType::File,
            num_replicas: config.required_replicas,
            duplicate_window: Duration::from_secs(7 * 86_400),
            description: Some("Zone-local StorageUsageReportV1 outbox".to_string()),
            ..Default::default()
        })
        .await
        .map_err(|error| format!("open storage report outbox: {error}"))?;
    js.get_or_create_stream(jetstream::stream::Config {
        name: OUTBOX_DLQ_STREAM.to_string(),
        subjects: vec![OUTBOX_DLQ_SUBJECT.to_string()],
        max_bytes: 64 * 1024 * 1024,
        max_age: Duration::from_secs(30 * 86_400),
        max_message_size: 16 * 1024,
        storage: StorageType::File,
        num_replicas: config.required_replicas,
        duplicate_window: Duration::from_secs(7 * 86_400),
        description: Some("Zone-local storage report quarantine".to_string()),
        ..Default::default()
    })
    .await
    .map_err(|error| format!("create storage report DLQ: {error}"))?;
    let consumer: PullConsumer = stream
        .get_or_create_consumer(
            OUTBOX_CONSUMER,
            jetstream::consumer::pull::Config {
                durable_name: Some(OUTBOX_CONSUMER.to_string()),
                ack_policy: AckPolicy::Explicit,
                ack_wait: Duration::from_secs(30),
                max_ack_pending: 1,
                ..Default::default()
            },
        )
        .await
        .map_err(|error| format!("create storage report outbox consumer: {error}"))?;
    let kafka = KafkaPublisher::connect(&settings).await?;
    tracing::info!(
        event_code = "ZONE_STORAGE_REPORT_RELAY_STARTED",
        zone_id = %settings.zone_id,
        outbox_stream = OUTBOX_STREAM,
        kafka_topic = %settings.report_topic()
    );
    relay_loop(
        settings,
        js,
        consumer,
        kafka,
        state,
        shutdown,
        assignment_epoch,
    )
    .await
}

async fn relay_loop(
    settings: Settings,
    js: async_nats::jetstream::Context,
    consumer: PullConsumer,
    kafka: KafkaPublisher,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    let mut messages = consumer
        .messages()
        .await
        .map_err(|error| format!("open Zone report outbox consumer: {error}"))?;
    loop {
        let next = tokio::select! {
            _ = shutdown.cancelled() => return Ok(()),
            next = messages.next() => next,
        };
        let Some(message) = next else {
            return Err("Zone report outbox consumer ended".to_string());
        };
        let message =
            message.map_err(|error| format!("read Zone report outbox message: {error}"))?;
        let payload_hash = Sha256::digest(&message.payload);
        let report = match StorageUsageReportV1::decode(message.payload.as_ref()) {
            Ok(report) => report,
            Err(_) => {
                quarantine_message(
                    &js,
                    &message.payload,
                    &settings.zone_id,
                    "STORAGE_USAGE_REPORT_PROTO_INVALID",
                    &payload_hash,
                )
                .await?;
                message
                    .ack()
                    .await
                    .map_err(|error| format!("ACK invalid Zone report outbox message: {error}"))?;
                continue;
            }
        };
        if let Err(reason) = validate_report(&report, settings.zone_id) {
            quarantine_message(
                &js,
                &message.payload,
                &settings.zone_id,
                reason,
                &payload_hash,
            )
            .await?;
            message
                .ack()
                .await
                .map_err(|error| format!("ACK rejected Zone report outbox message: {error}"))?;
            continue;
        }
        if !state
            .assignment_is_current("assignment.storage_report.0", assignment_epoch)
            .await?
        {
            return Ok(());
        }
        kafka.publish(&report).await?;
        message
            .ack()
            .await
            .map_err(|error| format!("ACK published Zone report outbox message: {error}"))?;
        tracing::info!(
            event_code = "ZONE_STORAGE_REPORT_KAFKA_PUBLISHED",
            report_id = %report.report_id,
            zone_id = %report.zone_id,
            payload_sha256 = %hex_digest(&payload_hash)
        );
    }
}

async fn quarantine_message(
    js: &async_nats::jetstream::Context,
    payload: &[u8],
    zone_id: &Uuid,
    reason: &'static str,
    payload_hash: &[u8],
) -> Result<(), String> {
    let record = DeadLetterRecord {
        schema_version: 1,
        source: "zone-control-storage-report-outbox",
        reason,
        payload_sha256: hex_digest(payload_hash),
        zone_id: zone_id.to_string(),
    };
    let bytes = serde_json::to_vec(&record)
        .map_err(|error| format!("encode Zone storage report DLQ record: {error}"))?;
    let message_id = format!("{}:{reason}", hex_digest(Sha256::digest(payload).as_ref()));
    let ack = js
        .send_publish(
            OUTBOX_DLQ_SUBJECT,
            PublishMessage::build()
                .payload(Bytes::from(bytes))
                .message_id(message_id),
        )
        .await
        .map_err(|error| format!("publish Zone storage report DLQ record: {error}"))?;
    ack.await
        .map_err(|error| format!("await Zone storage report DLQ acknowledgement: {error}"))?;
    Ok(())
}

async fn connect_nats(config: &Config) -> Result<async_nats::Client, String> {
    let options = async_nats::ConnectOptions::new()
        .add_root_certificates(config.nats_ca.clone())
        .require_tls(true)
        .add_client_certificate(config.nats_cert.clone(), config.nats_key.clone())
        .credentials_file(PathBuf::from(&config.nats_creds))
        .await
        .map_err(|error| format!("read storage report relay NATS credentials: {error}"))?;
    tokio::time::timeout(config.nats_timeout, options.connect(&config.nats_zone_url))
        .await
        .map_err(|_| "connect storage report relay NATS timed out".to_string())?
        .map_err(|error| format!("connect storage report relay NATS: {error}"))
}

fn required_env(name: &str) -> Result<String, String> {
    std::env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("{name} must be set and non-empty"))
}

fn optional_env(name: &str) -> Option<String> {
    std::env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn hex_digest(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}
