use std::{collections::BTreeMap, path::PathBuf, sync::Arc, time::Duration};

use async_nats::jetstream::{self, message::PublishMessage, stream::StorageType};
use bytes::Bytes;
use chrono::{DateTime, Utc};
use clickhouse::Client as ClickhouseClient;
use prost::Message;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    storage_usage_report_proto::{StorageUsageAggregateV1, StorageUsageReportV1},
    transfer_ticket::config::Config,
    zone_control_state::ZoneControlState,
};

const OUTBOX_STREAM: &str = "AURORA_ZONE_STORAGE_USAGE_OUTBOX";
const OUTBOX_SUBJECT_PREFIX: &str = "aurora.zone.storage.usage.report";
const REPORT_SCHEMA_VERSION: u32 = 1;
const STORAGE_METERING_MODULE: &str = "storage";
const STORAGE_METERING_SCHEMA: &str = "storage.access.completed.v1";
const MAX_REPORT_BYTES: usize = 512 * 1024;
const MAX_AGGREGATES: usize = 10_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_REPORT_AGE_MS: i64 = 30 * 86_400_000;
const REPORT_NAMESPACE: Uuid = Uuid::from_u128(0x5f0a_8e90_46e5_4fbb_8c01_7108_7f8c_1f22);

/// The metering publisher has its own settings and transport capabilities. It
/// deliberately does not share a context with transfer-ticket HTTP handlers or
/// the distributed Zone Control assignment scheduler.
#[derive(Clone)]
struct Settings {
    zone_id: Uuid,
    clickhouse_url: String,
    window: Duration,
    late_grace: Duration,
    poll_interval: Duration,
    max_backfill_windows: u32,
    storage_scan_shards: u16,
}

impl Settings {
    fn from_env(config: &Config) -> Result<Self, String> {
        let zone_id = Uuid::parse_str(&config.zone_id)
            .map_err(|_| "ZONE_ID is invalid for storage metering".to_string())?;
        if zone_id.is_nil() {
            return Err("ZONE_ID must be non-nil for storage metering".to_string());
        }
        let window_seconds = parse_env("METERING_WINDOW_SECONDS", 3_600_u64)?;
        if window_seconds != 3_600 {
            return Err(
                "METERING_WINDOW_SECONDS must be exactly 3600 for hourly settlement".to_string(),
            );
        }
        let late_grace_seconds =
            parse_env("METERING_LATE_GRACE_SECONDS", 300_u64)?.clamp(30, 3_600);
        if late_grace_seconds >= window_seconds {
            return Err("METERING_LATE_GRACE_SECONDS must be less than the window".to_string());
        }
        Ok(Self {
            zone_id,
            clickhouse_url: required_env("CLICKHOUSE_URL")?,
            window: Duration::from_secs(window_seconds),
            late_grace: Duration::from_secs(late_grace_seconds),
            poll_interval: Duration::from_secs(
                parse_env("METERING_PUBLISH_INTERVAL_SECONDS", 30_u64)?.clamp(5, 3_600),
            ),
            max_backfill_windows: parse_env("METERING_MAX_BACKFILL_WINDOWS", 720_u32)?
                .clamp(1, 720),
            storage_scan_shards: parse_env("ZONE_CONTROL_ASSIGNMENT_SHARDS", 16_u16)?.clamp(1, 256),
        })
    }

    fn outbox_subject(&self) -> String {
        format!("{OUTBOX_SUBJECT_PREFIX}.{}", self.zone_id)
    }
}

#[derive(Debug, Deserialize, clickhouse::Row)]
struct AccessAggregateRow {
    resource_id: Uuid,
    upload_bytes: u64,
    download_bytes: u64,
    request_count: u64,
}

#[derive(Debug, Deserialize, clickhouse::Row)]
struct CapacityAggregateRow {
    bucket_name: String,
    storage_bytes: u64,
}

#[derive(Debug, Deserialize, clickhouse::Row)]
struct CapacityShardCountRow {
    completed_shards: u64,
}

/// Starts the Zone-local report publisher. The caller keeps this workflow
/// opt-in while the Cost Engine cutover remains a separate deployment gate.
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
        .map_err(|error| format!("ZONE_STORAGE_METERING_CONFIG_INVALID: {error}"))?;
    run_generation(&config, settings, state, shutdown, assignment_epoch).await
}

async fn run_generation(
    config: &Config,
    settings: Settings,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    let client = connect_nats(config).await?;
    let js = jetstream::new(client);
    js.get_or_create_stream(jetstream::stream::Config {
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
    let clickhouse = ClickhouseClient::default()
        .with_url(&settings.clickhouse_url)
        .with_database("storage");
    tracing::info!(
        event_code = "ZONE_STORAGE_METERING_STARTED",
        zone_id = %settings.zone_id,
        outbox_stream = OUTBOX_STREAM,
        window_seconds = settings.window.as_secs(),
        late_grace_seconds = settings.late_grace.as_secs()
    );
    aggregate_loop(settings, js, clickhouse, state, shutdown, assignment_epoch).await
}

async fn aggregate_loop(
    settings: Settings,
    js: async_nats::jetstream::Context,
    clickhouse: ClickhouseClient,
    state: Arc<ZoneControlState>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
) -> Result<(), String> {
    let mut interval = tokio::time::interval(settings.poll_interval);
    let mut startup_backfill = true;
    interval.tick().await;
    loop {
        tokio::select! {
            _ = shutdown.cancelled() => return Ok(()),
            _ = interval.tick() => {
                if !state.assignment_is_current("assignment.storage_report.0", assignment_epoch).await? {
                    return Ok(());
                }
                let backfill_windows = if startup_backfill {
                    settings.max_backfill_windows
                } else {
                    settings.max_backfill_windows.min(24)
                };
                match publish_closed_windows(&settings, &js, &clickhouse, backfill_windows).await {
                    Ok(()) => startup_backfill = false,
                    Err(error) => tracing::warn!(event_code = "ZONE_STORAGE_METERING_AGGREGATION_FAILED", error = %error),
                }
            }
        }
    }
}

async fn publish_closed_windows(
    settings: &Settings,
    js: &async_nats::jetstream::Context,
    clickhouse: &ClickhouseClient,
    backfill_windows: u32,
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
    for backfill in 0..backfill_windows {
        let end_ms = eligible_end_ms.saturating_sub(i64::from(backfill).saturating_mul(window_ms));
        let start_ms = end_ms.saturating_sub(window_ms);
        let window_start = DateTime::<Utc>::from_timestamp_millis(start_ms)
            .ok_or_else(|| "closed metering window start is invalid".to_string())?;
        let window_end = DateTime::<Utc>::from_timestamp_millis(end_ms)
            .ok_or_else(|| "closed metering window end is invalid".to_string())?;
        let Some(aggregates) = read_closed_window(
            clickhouse,
            settings.zone_id,
            window_start,
            window_end,
            settings.storage_scan_shards,
        )
        .await?
        else {
            continue;
        };
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
    expected_shards: u16,
) -> Result<Option<Vec<StorageUsageAggregateV1>>, String> {
    let completion_query = format!(
        "SELECT countDistinct(shard_id) AS completed_shards \
         FROM storage.bucket_capacity_scan_completions \
         WHERE zone_id = toUUID('{}') \
           AND billing_window_end = toDateTime64('{}', 3, 'UTC')",
        zone_id,
        window_end.format("%Y-%m-%d %H:%M:%S%.3f")
    );
    let completed = clickhouse
        .query(&completion_query)
        .fetch_one::<CapacityShardCountRow>()
        .await
        .map_err(|error| format!("read Zone capacity scan completion: {error}"))?;
    if completed.completed_shards < u64::from(expected_shards) {
        return Ok(None);
    }

    let transfer_query = format!(
        "SELECT resource_id, sum(bytes_received) AS upload_bytes, \
         sum(bytes_sent) AS download_bytes, count() AS request_count \
         FROM storage.access_event_journal FINAL \
         WHERE zone_id = toUUID('{}') \
           AND module = '{}' \
           AND metering_schema = '{}' \
           AND resource_id != toUUID('00000000-0000-0000-0000-000000000000') \
           AND timestamp >= toDateTime64('{}', 3, 'UTC') \
           AND timestamp < toDateTime64('{}', 3, 'UTC') \
           AND status >= 200 AND status < 300 \
           AND method IN ('GET', 'PUT') \
         GROUP BY resource_id ORDER BY resource_id ASC",
        zone_id,
        STORAGE_METERING_MODULE,
        STORAGE_METERING_SCHEMA,
        window_start.format("%Y-%m-%d %H:%M:%S%.3f"),
        window_end.format("%Y-%m-%d %H:%M:%S%.3f")
    );
    let mut cursor = clickhouse
        .query(&transfer_query)
        .fetch::<AccessAggregateRow>()
        .map_err(|error| format!("read Zone ClickHouse closed window: {error}"))?;
    let mut result = BTreeMap::<String, StorageUsageAggregateV1>::new();
    while let Some(row) = cursor
        .next()
        .await
        .map_err(|error| format!("read Zone ClickHouse aggregate row: {error}"))?
    {
        if row.resource_id.is_nil() {
            continue;
        }
        if row.upload_bytes == 0 && row.download_bytes == 0 {
            continue;
        }
        if result.len() >= MAX_AGGREGATES {
            return Err("closed window exceeds storage report aggregate limit".to_string());
        }
        result.insert(
            row.resource_id.to_string(),
            StorageUsageAggregateV1 {
                resource_id: row.resource_id.to_string(),
                upload_bytes: row.upload_bytes,
                download_bytes: row.download_bytes,
                request_count: row.request_count,
                resource_name: String::new(),
                storage_bytes: 0,
                storage_byte_hours: 0,
            },
        );
    }
    let capacity_query = format!(
        "SELECT bucket_name, argMax(used_bytes, observed_at) AS storage_bytes \
         FROM storage.bucket_capacity_journal \
         WHERE zone_id = toUUID('{}') \
           AND billing_window_end = toDateTime64('{}', 3, 'UTC') \
         GROUP BY bucket_name ORDER BY bucket_name ASC",
        zone_id,
        window_end.format("%Y-%m-%d %H:%M:%S%.3f")
    );
    let mut capacity_cursor = clickhouse
        .query(&capacity_query)
        .fetch::<CapacityAggregateRow>()
        .map_err(|error| format!("read Zone capacity aggregate: {error}"))?;
    while let Some(row) = capacity_cursor
        .next()
        .await
        .map_err(|error| format!("read Zone capacity aggregate row: {error}"))?
    {
        if result.len() >= MAX_AGGREGATES {
            return Err("closed window exceeds storage report aggregate limit".to_string());
        }
        if row.storage_bytes == 0 {
            continue;
        }
        result.insert(
            format!("name:{}", row.bucket_name),
            StorageUsageAggregateV1 {
                resource_id: String::new(),
                upload_bytes: 0,
                download_bytes: 0,
                request_count: 0,
                resource_name: row.bucket_name,
                storage_bytes: row.storage_bytes,
                storage_byte_hours: row.storage_bytes,
            },
        );
    }
    Ok(Some(result.into_values().collect()))
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

pub(crate) fn validate_report(
    report: &StorageUsageReportV1,
    expected_zone: Uuid,
) -> Result<(), &'static str> {
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
        || report.window_end_unix_ms - report.window_start_unix_ms != 3_600_000
        || report.window_start_unix_ms.rem_euclid(3_600_000) != 0
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
        || report.window_start_unix_ms < now.saturating_sub(MAX_REPORT_AGE_MS)
    {
        return Err("STORAGE_USAGE_REPORT_TIME_INVALID");
    }
    let expected_sequence = u64::try_from(report.window_end_unix_ms.div_euclid(3_600_000))
        .map_err(|_| "STORAGE_USAGE_REPORT_SEQUENCE_INVALID")?;
    let expected_report_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{}:{}:{}:{}",
            zone_id, report.window_start_unix_ms, report.window_end_unix_ms, expected_sequence
        )
        .as_bytes(),
    );
    if report.sequence != expected_sequence || report_id != expected_report_id {
        return Err("STORAGE_USAGE_REPORT_SEQUENCE_INVALID");
    }
    let mut last_key: Option<String> = None;
    for aggregate in &report.aggregates {
        let key = if !aggregate.resource_id.is_empty() {
            let resource_id = Uuid::parse_str(&aggregate.resource_id)
                .map_err(|_| "STORAGE_USAGE_REPORT_RESOURCE_INVALID")?;
            if resource_id.is_nil() {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
            }
            format!("id:{resource_id}")
        } else if !aggregate.resource_name.is_empty() {
            if aggregate.resource_name.len() > 255
                || (!aggregate.resource_name.starts_with("ws-")
                    && !aggregate.resource_name.starts_with("tn-"))
            {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_NAME_INVALID");
            }
            format!("name:{}", aggregate.resource_name)
        } else {
            return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
        };
        if let Some(previous) = last_key.as_ref() {
            if &key <= previous {
                return Err("STORAGE_USAGE_REPORT_RESOURCE_ORDER_INVALID");
            }
        }
        last_key = Some(key);
        if i64::try_from(aggregate.download_bytes).is_err()
            || i64::try_from(aggregate.upload_bytes).is_err()
            || i64::try_from(aggregate.request_count).is_err()
            || i64::try_from(aggregate.storage_bytes).is_err()
            || i64::try_from(aggregate.storage_byte_hours).is_err()
        {
            return Err("STORAGE_USAGE_REPORT_NUMERIC_INVALID");
        }
        let capacity = aggregate.storage_byte_hours > 0;
        let transfer = aggregate.upload_bytes > 0 || aggregate.download_bytes > 0;
        if capacity
            && (aggregate.resource_name.is_empty()
                || !aggregate.resource_id.is_empty()
                || transfer
                || aggregate.request_count != 0
                || aggregate.storage_bytes != aggregate.storage_byte_hours)
        {
            return Err("STORAGE_USAGE_REPORT_CAPACITY_INVALID");
        }
        if transfer
            && (aggregate.resource_id.is_empty()
                || !aggregate.resource_name.is_empty()
                || aggregate.request_count == 0
                || aggregate.storage_bytes != 0
                || aggregate.storage_byte_hours != 0)
        {
            return Err("STORAGE_USAGE_REPORT_TRANSFER_INVALID");
        }
        if !capacity && !transfer {
            return Err("STORAGE_USAGE_REPORT_QUANTITY_INVALID");
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

fn parse_env<T: std::str::FromStr>(name: &str, default: T) -> Result<T, String> {
    match std::env::var(name) {
        Ok(value) => value.parse().map_err(|_| format!("{name} is invalid")),
        Err(_) => Ok(default),
    }
}

#[cfg(test)]
#[path = "../tests/unit/metering.rs"]
mod tests;
