use std::{path::PathBuf, sync::Arc, time::Duration};

use async_nats::jetstream::{
    self,
    consumer::{AckPolicy, PullConsumer},
    message::PublishMessage,
    stream::StorageType,
};
use bytes::Bytes;
use chrono::{DateTime, Utc};
use clickhouse::Client as ClickhouseClient;
use futures_util::StreamExt;
use krafka::{
    auth::{AuthConfig, TlsConfig},
    producer::{Acks, Producer},
    protocol::Compression,
};
use prost::Message;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    storage_usage_report_proto::{StorageUsageAggregateV1, StorageUsageReportV1},
    transfer_ticket::config::Config,
};

const OUTBOX_STREAM: &str = "AURORA_ZONE_STORAGE_USAGE_OUTBOX";
const OUTBOX_SUBJECT_PREFIX: &str = "aurora.zone.storage.usage.report";
const OUTBOX_DLQ_STREAM: &str = "AURORA_ZONE_STORAGE_USAGE_DLQ";
const OUTBOX_DLQ_SUBJECT: &str = "aurora.zone.storage.usage.dlq";
const OUTBOX_CONSUMER: &str = "zone-control-storage-report-kafka-v1";
const REPORT_TOPIC_SUFFIX: &str = "storage.usage.reports.v1";
const REPORT_SCHEMA_VERSION: u32 = 1;
const MAX_REPORT_BYTES: usize = 512 * 1024;
const MAX_AGGREGATES: usize = 10_000;
const MAX_REPORT_WINDOW_MS: i64 = 86_400_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const REPORT_NAMESPACE: Uuid = Uuid::from_u128(0x5f0a_8e90_46e5_4fbb_8c01_7108_7f8c_1f22);

/// The metering publisher has its own settings and transport capabilities. It
/// deliberately does not share a context with transfer-ticket HTTP handlers or
/// the Zone leader coordinator.
#[derive(Clone)]
struct Settings {
    zone_id: Uuid,
    clickhouse_url: String,
    kafka_bootstrap_servers: String,
    kafka_security_protocol: String,
    kafka_username: Option<String>,
    kafka_password: Option<String>,
    kafka_ca_cert: Option<String>,
    kafka_topic_prefix: String,
    window: Duration,
    late_grace: Duration,
    poll_interval: Duration,
    max_backfill_windows: u32,
}

impl Settings {
    fn from_env(config: &Config) -> Result<Self, String> {
        let zone_id = Uuid::parse_str(&config.zone_id)
            .map_err(|_| "ZONE_ID is invalid for storage metering".to_string())?;
        if zone_id.is_nil() {
            return Err("ZONE_ID must be non-nil for storage metering".to_string());
        }
        let window_seconds = parse_env("METERING_WINDOW_SECONDS", 3_600_u64)?.clamp(60, 86_400);
        let late_grace_seconds =
            parse_env("METERING_LATE_GRACE_SECONDS", 300_u64)?.clamp(30, 3_600);
        if late_grace_seconds >= window_seconds {
            return Err("METERING_LATE_GRACE_SECONDS must be less than the window".to_string());
        }
        Ok(Self {
            zone_id,
            clickhouse_url: required_env("CLICKHOUSE_URL")?,
            kafka_bootstrap_servers: required_env("KAFKA_BOOTSTRAP_SERVERS")?,
            kafka_security_protocol: required_env("KAFKA_SECURITY_PROTOCOL")?.to_ascii_lowercase(),
            kafka_username: optional_env("KAFKA_USERNAME"),
            kafka_password: optional_env("KAFKA_PASSWORD"),
            kafka_ca_cert: optional_env("KAFKA_CA_CERT"),
            kafka_topic_prefix: required_env("KAFKA_TOPIC_PREFIX")?
                .trim_end_matches('.')
                .to_string(),
            window: Duration::from_secs(window_seconds),
            late_grace: Duration::from_secs(late_grace_seconds),
            poll_interval: Duration::from_secs(
                parse_env("METERING_PUBLISH_INTERVAL_SECONDS", 30_u64)?.clamp(5, 3_600),
            ),
            max_backfill_windows: parse_env("METERING_MAX_BACKFILL_WINDOWS", 24_u32)?.clamp(1, 168),
        })
    }

    fn outbox_subject(&self) -> String {
        format!("{OUTBOX_SUBJECT_PREFIX}.{}", self.zone_id)
    }

    fn report_topic(&self) -> String {
        format!("{}.{}", self.kafka_topic_prefix, REPORT_TOPIC_SUFFIX)
    }
}

#[derive(Debug, Deserialize, clickhouse::Row)]
struct AccessAggregateRow {
    resource_id: Uuid,
    upload_bytes: u64,
    download_bytes: u64,
    request_count: u64,
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
            .client_id(format!("aurora-zone-control-metering-{}", settings.zone_id))
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

/// Starts the Zone-local report publisher. The caller keeps this workflow
/// opt-in so Dataplane's legacy billing/leader duty cannot run a second
/// charge-producing path during the migration.
pub async fn run(config: Config, shutdown: CancellationToken) -> Result<(), String> {
    if shutdown.is_cancelled() {
        return Ok(());
    }
    let settings = Settings::from_env(&config)
        .map_err(|error| format!("ZONE_STORAGE_METERING_CONFIG_INVALID: {error}"))?;
    run_generation(&config, settings, shutdown).await
}

async fn run_generation(
    config: &Config,
    settings: Settings,
    shutdown: CancellationToken,
) -> Result<(), String> {
    let client = connect_nats(config).await?;
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
        .map_err(|error| format!("create storage report outbox: {error}"))?;
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
    let clickhouse = ClickhouseClient::default()
        .with_url(&settings.clickhouse_url)
        .with_database("storage");
    let kafka = KafkaPublisher::connect(&settings).await?;
    tracing::info!(
        event_code = "ZONE_STORAGE_METERING_STARTED",
        zone_id = %settings.zone_id,
        outbox_stream = OUTBOX_STREAM,
        kafka_topic = %settings.report_topic(),
        window_seconds = settings.window.as_secs(),
        late_grace_seconds = settings.late_grace.as_secs()
    );

    let mut aggregation = tokio::spawn(aggregate_loop(
        settings.clone(),
        js.clone(),
        clickhouse,
        shutdown.clone(),
    ));
    let mut relay = tokio::spawn(relay_loop(settings, js, consumer, kafka, shutdown.clone()));
    tokio::select! {
        result = &mut aggregation => {
            relay.abort();
            result.map_err(|error| format!("storage report aggregation task: {error}"))??;
        }
        result = &mut relay => {
            aggregation.abort();
            result.map_err(|error| format!("storage report Kafka relay task: {error}"))??;
        }
        _ = shutdown.cancelled() => {
            aggregation.abort();
            relay.abort();
            return Ok(());
        }
    }
    Ok(())
}

async fn aggregate_loop(
    settings: Settings,
    js: async_nats::jetstream::Context,
    clickhouse: ClickhouseClient,
    shutdown: CancellationToken,
) -> Result<(), String> {
    let mut interval = tokio::time::interval(settings.poll_interval);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = shutdown.cancelled() => return Ok(()),
            _ = interval.tick() => {
                if let Err(error) = publish_closed_windows(&settings, &js, &clickhouse).await {
                    tracing::warn!(event_code = "ZONE_STORAGE_METERING_AGGREGATION_FAILED", error = %error);
                }
            }
        }
    }
}

async fn publish_closed_windows(
    settings: &Settings,
    js: &async_nats::jetstream::Context,
    clickhouse: &ClickhouseClient,
) -> Result<(), String> {
    let now = Utc::now();
    let window_ms = i64::try_from(settings.window.as_millis())
        .map_err(|_| "metering window exceeds i64 milliseconds".to_string())?;
    let grace_ms = i64::try_from(settings.late_grace.as_millis())
        .map_err(|_| "metering grace exceeds i64 milliseconds".to_string())?;
    let eligible_end_ms = now
        .timestamp_millis()
        .saturating_sub(grace_ms)
        .div_euclid(window_ms)
        .saturating_mul(window_ms);
    for backfill in 0..settings.max_backfill_windows {
        let end_ms = eligible_end_ms.saturating_sub(i64::from(backfill).saturating_mul(window_ms));
        let start_ms = end_ms.saturating_sub(window_ms);
        let window_start = DateTime::<Utc>::from_timestamp_millis(start_ms)
            .ok_or_else(|| "closed metering window start is invalid".to_string())?;
        let window_end = DateTime::<Utc>::from_timestamp_millis(end_ms)
            .ok_or_else(|| "closed metering window end is invalid".to_string())?;
        let aggregates =
            read_closed_window(clickhouse, settings.zone_id, window_start, window_end).await?;
        if aggregates.is_empty() {
            continue;
        }
        let sequence = u64::try_from(end_ms.div_euclid(window_ms))
            .map_err(|_| "metering sequence is negative".to_string())?;
        let report_id = Uuid::new_v5(
            &REPORT_NAMESPACE,
            format!("{}:{start_ms}:{end_ms}:{sequence}", settings.zone_id).as_bytes(),
        );
        let mut report = StorageUsageReportV1 {
            schema_version: REPORT_SCHEMA_VERSION,
            report_id: report_id.to_string(),
            zone_id: settings.zone_id.to_string(),
            window_start_unix_ms: start_ms,
            window_end_unix_ms: end_ms,
            sequence,
            correction: false,
            aggregates,
            report_sha256: Vec::new(),
            correction_of_report_id: String::new(),
        };
        let payload = canonical_report_payload(&mut report)?;
        let ack = js
            .send_publish(
                settings.outbox_subject(),
                PublishMessage::build()
                    .payload(Bytes::from(payload))
                    .message_id(report.report_id.clone()),
            )
            .await
            .map_err(|error| format!("persist storage report in Zone outbox: {error}"))?;
        ack.await
            .map_err(|error| format!("await Zone report outbox acknowledgement: {error}"))?;
        tracing::info!(
            event_code = "ZONE_STORAGE_REPORT_OUTBOXED",
            report_id = %report.report_id,
            zone_id = %report.zone_id,
            window_start = %window_start,
            window_end = %window_end,
            aggregate_count = report.aggregates.len()
        );
    }
    Ok(())
}

async fn read_closed_window(
    clickhouse: &ClickhouseClient,
    zone_id: Uuid,
    window_start: DateTime<Utc>,
    window_end: DateTime<Utc>,
) -> Result<Vec<StorageUsageAggregateV1>, String> {
    let query = format!(
        "SELECT resource_id, sum(bytes_received) AS upload_bytes, \
         sum(bytes_sent) AS download_bytes, count() AS request_count \
         FROM storage.access_event_journal FINAL \
         WHERE zone_id = toUUID('{}') \
           AND resource_id != toUUID('00000000-0000-0000-0000-000000000000') \
           AND timestamp >= toDateTime64('{}', 3, 'UTC') \
           AND timestamp < toDateTime64('{}', 3, 'UTC') \
           AND status >= 200 AND status < 300 \
           AND method IN ('GET', 'PUT') \
         GROUP BY resource_id ORDER BY resource_id ASC",
        zone_id,
        window_start.format("%Y-%m-%d %H:%M:%S%.3f"),
        window_end.format("%Y-%m-%d %H:%M:%S%.3f")
    );
    let mut cursor = clickhouse
        .query(&query)
        .fetch::<AccessAggregateRow>()
        .map_err(|error| format!("read Zone ClickHouse closed window: {error}"))?;
    let mut result = Vec::new();
    while let Some(row) = cursor
        .next()
        .await
        .map_err(|error| format!("read Zone ClickHouse aggregate row: {error}"))?
    {
        if row.resource_id.is_nil() {
            continue;
        }
        if result.len() >= MAX_AGGREGATES {
            return Err("closed window exceeds storage report aggregate limit".to_string());
        }
        result.push(StorageUsageAggregateV1 {
            resource_id: row.resource_id.to_string(),
            upload_bytes: row.upload_bytes,
            download_bytes: row.download_bytes,
            request_count: row.request_count,
        });
    }
    Ok(result)
}

async fn relay_loop(
    settings: Settings,
    js: async_nats::jetstream::Context,
    consumer: PullConsumer,
    kafka: KafkaPublisher,
    shutdown: CancellationToken,
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

fn canonical_report_payload(report: &mut StorageUsageReportV1) -> Result<Vec<u8>, String> {
    if report.report_sha256.is_empty() {
        report.report_sha256 = vec![0; 32];
    }
    validate_report_shape(report)?;
    report.report_sha256.clear();
    let digest = Sha256::digest(report.encode_to_vec());
    report.report_sha256 = digest.to_vec();
    let payload = report.encode_to_vec();
    if payload.len() > MAX_REPORT_BYTES {
        return Err("STORAGE_USAGE_REPORT_SIZE_INVALID".to_string());
    }
    Ok(payload)
}

fn validate_report(report: &StorageUsageReportV1, expected_zone: Uuid) -> Result<(), &'static str> {
    validate_report_shape(report)?;
    if report.zone_id != expected_zone.to_string() {
        return Err("STORAGE_USAGE_REPORT_ZONE_MISMATCH");
    }
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    if report.report_sha256.as_slice() != Sha256::digest(canonical.encode_to_vec()).as_slice() {
        return Err("STORAGE_USAGE_REPORT_CHECKSUM_INVALID");
    }
    Ok(())
}

fn validate_report_shape(report: &StorageUsageReportV1) -> Result<(), &'static str> {
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "STORAGE_USAGE_REPORT_ID_INVALID")?;
    let zone_id =
        Uuid::parse_str(&report.zone_id).map_err(|_| "STORAGE_USAGE_REPORT_ZONE_INVALID")?;
    if report.schema_version != REPORT_SCHEMA_VERSION
        || report_id.is_nil()
        || zone_id.is_nil()
        || report.window_end_unix_ms <= report.window_start_unix_ms
        || report.window_end_unix_ms - report.window_start_unix_ms > MAX_REPORT_WINDOW_MS
        || report.aggregates.is_empty()
        || report.aggregates.len() > MAX_AGGREGATES
        || report.report_sha256.len() != 32
        || report.correction
        || !report.correction_of_report_id.is_empty()
    {
        return Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID");
    }
    let now = Utc::now().timestamp_millis();
    if report.window_end_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || report.window_start_unix_ms < now.saturating_sub(7 * 86_400_000)
    {
        return Err("STORAGE_USAGE_REPORT_TIME_INVALID");
    }
    let mut last_resource = None;
    for aggregate in &report.aggregates {
        let resource_id = Uuid::parse_str(&aggregate.resource_id)
            .map_err(|_| "STORAGE_USAGE_REPORT_RESOURCE_INVALID")?;
        if resource_id.is_nil() {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
        }
        if let Some(previous) = last_resource {
            if resource_id <= previous {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_ORDER_INVALID");
            }
        }
        last_resource = Some(resource_id);
        if i64::try_from(aggregate.download_bytes).is_err()
            || i64::try_from(aggregate.upload_bytes).is_err()
            || i64::try_from(aggregate.request_count).is_err()
        {
            return Err("STORAGE_USAGE_REPORT_NUMERIC_INVALID");
        }
    }
    Ok(())
}

async fn connect_nats(config: &Config) -> Result<async_nats::Client, String> {
    let options = async_nats::ConnectOptions::new()
        .add_root_certificates(config.nats_ca.clone())
        .require_tls(true)
        .add_client_certificate(config.nats_cert.clone(), config.nats_key.clone())
        .credentials_file(PathBuf::from(&config.nats_creds))
        .await
        .map_err(|error| format!("read Zone Control NATS credentials: {error}"))?;
    tokio::time::timeout(config.nats_timeout, options.connect(&config.nats_zone_url))
        .await
        .map_err(|_| "connect Zone metering NATS timed out".to_string())?
        .map_err(|error| format!("connect Zone metering NATS: {error}"))
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

fn parse_env<T: std::str::FromStr>(name: &str, default: T) -> Result<T, String> {
    match std::env::var(name) {
        Ok(value) => value.parse().map_err(|_| format!("{name} is invalid")),
        Err(_) => Ok(default),
    }
}

fn hex_digest(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn report() -> StorageUsageReportV1 {
        let now = Utc::now().timestamp_millis();
        let mut report = StorageUsageReportV1 {
            schema_version: REPORT_SCHEMA_VERSION,
            report_id: Uuid::new_v4().to_string(),
            zone_id: Uuid::new_v4().to_string(),
            window_start_unix_ms: now - 60_000,
            window_end_unix_ms: now - 1_000,
            sequence: 1,
            correction: false,
            aggregates: vec![StorageUsageAggregateV1 {
                resource_id: Uuid::new_v4().to_string(),
                upload_bytes: 2,
                download_bytes: 42,
                request_count: 1,
            }],
            report_sha256: vec![0; 32],
            correction_of_report_id: String::new(),
        };
        let mut unsigned = report.clone();
        unsigned.report_sha256.clear();
        report.report_sha256 = Sha256::digest(unsigned.encode_to_vec()).to_vec();
        report
    }

    #[test]
    fn canonical_report_is_deterministic_and_zone_bound() {
        let report = report();
        let zone_id = Uuid::parse_str(&report.zone_id).unwrap();
        assert!(validate_report(&report, zone_id).is_ok());
        let mut canonical = report.clone();
        assert_eq!(
            canonical_report_payload(&mut canonical).unwrap(),
            report.encode_to_vec()
        );
    }

    #[test]
    fn report_rejects_unsorted_resource_lines() {
        let mut report = report();
        report.aggregates[0].resource_id = "00000000-0000-0000-0000-000000000002".to_string();
        let second = StorageUsageAggregateV1 {
            resource_id: "00000000-0000-0000-0000-000000000001".to_string(),
            upload_bytes: 0,
            download_bytes: 1,
            request_count: 1,
        };
        report.aggregates.push(second);
        assert_eq!(
            validate_report_shape(&report),
            Err("STORAGE_USAGE_REPORT_RESOURCE_ORDER_INVALID")
        );
    }

    #[test]
    fn report_rejects_correction_for_initial_publisher() {
        let mut report = report();
        report.correction = true;
        assert_eq!(
            validate_report_shape(&report),
            Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID")
        );
    }
}
