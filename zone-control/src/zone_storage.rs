use aws_config::BehaviorVersion;
use aws_credential_types::Credentials;
use aws_sdk_s3::config::{Builder, Region};
use aws_sdk_s3::Client as S3Client;
use chrono::{DateTime, Utc};
use clickhouse::Client as ClickhouseClient;
use prost::Message;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::sync::Arc;
use std::time::Duration;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::transfer_ticket::config::Config;
use crate::transport_proto::{BucketSizeV1, StorageBucketSizesSnapshotV1};
use crate::zone_control_kafka::ControlKafka;
use crate::zone_control_state::ZoneControlState;

pub(crate) async fn run_bucket_scanner(
    config: Config,
    state: Arc<ZoneControlState>,
    kafka: Arc<ControlKafka>,
    shutdown: CancellationToken,
    assignment_epoch: u64,
    shard_id: u16,
) -> Result<(), String> {
    let assignment_key = format!("assignment.storage_scan.{shard_id}");
    let zone_id = Uuid::parse_str(&config.zone_id)
        .map_err(|_| "ZONE_ID is invalid for storage scan".to_string())?;
    let clickhouse = ClickhouseClient::default()
        .with_url(&config.clickhouse_url)
        .with_database("storage");
    loop {
        let delay = if config.storage_scan_interval == Duration::from_secs(3_600) {
            let now = Utc::now().timestamp().rem_euclid(3_600) as u64;
            Duration::from_secs(3_600 - now)
        } else {
            config.storage_scan_interval
        };
        if !wait_or_cancel(&shutdown, delay).await {
            return Ok(());
        }
        let metadata = state.read_metadata().await?;
        if metadata.status != "active"
            || !metadata.services.get("storage").copied().unwrap_or(false)
        {
            continue;
        }
        let Some(buckets) =
            scan_all_customer_storage_bucket_sizes(&config, &state, assignment_epoch, shard_id)
                .await?
        else {
            return Ok(());
        };
        let observed_at = Utc::now();
        let billing_window_end =
            DateTime::<Utc>::from_timestamp(observed_at.timestamp().div_euclid(3_600) * 3_600, 0)
                .ok_or_else(|| "storage scan billing window is invalid".to_string())?;
        let scan_generation = Uuid::new_v4().to_string();
        if !state
            .assignment_is_current(&assignment_key, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        persist_capacity_scan(
            &clickhouse,
            zone_id,
            shard_id,
            &scan_generation,
            observed_at,
            billing_window_end,
            &buckets,
        )
        .await?;
        if buckets.is_empty() {
            continue;
        }
        let snapshot = StorageBucketSizesSnapshotV1 {
            event_id: Uuid::new_v4().as_bytes().to_vec(),
            zone_id: zone_id.as_bytes().to_vec(),
            observed_at_unix_ms: observed_at.timestamp_millis(),
            buckets: buckets
                .into_iter()
                .map(|(bucket_name, size_bytes)| BucketSizeV1 {
                    bucket_name,
                    size_bytes,
                })
                .collect(),
            schema_version: 1,
        };
        let mut payload = Vec::with_capacity(snapshot.encoded_len());
        snapshot
            .encode(&mut payload)
            .map_err(|error| format!("encode storage sizes snapshot: {error}"))?;
        if !state
            .assignment_is_current(&assignment_key, assignment_epoch)
            .await?
        {
            return Ok(());
        }
        kafka
            .publish(
                &kafka.storage_sizes_topic(),
                config.zone_id.as_bytes(),
                &payload,
            )
            .await
            .map_err(|error| {
                format!("publish storage sizes snapshot at assignment {assignment_epoch}: {error}")
            })?;
    }
}

#[derive(clickhouse::Row, Serialize)]
struct CapacityJournalRow {
    observed_at: DateTime<Utc>,
    billing_window_end: DateTime<Utc>,
    zone_id: Uuid,
    bucket_name: String,
    used_bytes: u64,
    scan_generation: String,
    shard_id: u16,
}

#[derive(clickhouse::Row, Serialize)]
struct CapacityScanCompletionRow {
    completed_at: DateTime<Utc>,
    billing_window_end: DateTime<Utc>,
    zone_id: Uuid,
    scan_generation: String,
    shard_id: u16,
}

async fn persist_capacity_scan(
    clickhouse: &ClickhouseClient,
    zone_id: Uuid,
    shard_id: u16,
    scan_generation: &str,
    observed_at: DateTime<Utc>,
    billing_window_end: DateTime<Utc>,
    buckets: &[(String, i64)],
) -> Result<(), String> {
    if !buckets.is_empty() {
        let mut rows = clickhouse
            .insert("storage.bucket_capacity_journal")
            .map_err(|error| format!("open Zone capacity journal insert: {error}"))?;
        for (bucket_name, used_bytes) in buckets {
            rows.write(&CapacityJournalRow {
                observed_at,
                billing_window_end,
                zone_id,
                bucket_name: bucket_name.clone(),
                used_bytes: u64::try_from(*used_bytes)
                    .map_err(|_| format!("negative capacity for bucket {bucket_name}"))?,
                scan_generation: scan_generation.to_string(),
                shard_id,
            })
            .await
            .map_err(|error| format!("write Zone capacity journal row: {error}"))?;
        }
        rows.end()
            .await
            .map_err(|error| format!("commit Zone capacity journal insert: {error}"))?;
    }

    let mut completion = clickhouse
        .insert("storage.bucket_capacity_scan_completions")
        .map_err(|error| format!("open Zone capacity completion insert: {error}"))?;
    completion
        .write(&CapacityScanCompletionRow {
            completed_at: observed_at,
            billing_window_end,
            zone_id,
            scan_generation: scan_generation.to_string(),
            shard_id,
        })
        .await
        .map_err(|error| format!("write Zone capacity completion row: {error}"))?;
    completion
        .end()
        .await
        .map_err(|error| format!("commit Zone capacity completion insert: {error}"))?;
    Ok(())
}

async fn scan_all_customer_storage_bucket_sizes(
    config: &Config,
    state: &Arc<ZoneControlState>,
    assignment_epoch: u64,
    shard_id: u16,
) -> Result<Option<Vec<(String, i64)>>, String> {
    let (Some(host), Some(port), Some(access_key), Some(secret_key)) = (
        config.minio_host.as_deref(),
        config.minio_port,
        config.minio_access_key.as_deref(),
        config.minio_secret_key.as_deref(),
    ) else {
        return Err("MinIO storage scanner requires MINIO_HOST, MINIO_PORT, MINIO_ACCESS_KEY and MINIO_SECRET_KEY".to_string());
    };
    let credentials = Credentials::new(access_key, secret_key, None, None, "zone-control-minio");
    let sdk_config = aws_config::defaults(BehaviorVersion::latest())
        .credentials_provider(credentials)
        .endpoint_url(format!("http://{host}:{port}"))
        .region(Region::new("us-east-1"))
        .load()
        .await;
    let client = S3Client::from_conf(Builder::from(&sdk_config).force_path_style(true).build());
    let response = client
        .list_buckets()
        .send()
        .await
        .map_err(|error| format!("list MinIO buckets: {error}"))?;
    let shard_count = u16::try_from(config.control_assignment_shards)
        .map_err(|_| "storage scan shard count exceeds u16".to_string())?;
    if shard_count == 0 || shard_id >= shard_count {
        return Err("storage scan shard is outside configured range".to_string());
    }
    let mut names = response
        .buckets
        .unwrap_or_default()
        .into_iter()
        .filter_map(|bucket| bucket.name)
        .filter(|name| {
            (name.starts_with("ws-") || name.starts_with("tn-"))
                && bucket_shard(name, shard_count) == shard_id
        })
        .collect::<Vec<_>>();
    names.sort();
    let mut sizes = Vec::with_capacity(names.len());
    for chunk in names.chunks(config.storage_scan_batch_size) {
        if !state
            .assignment_is_current(
                &format!("assignment.storage_scan.{shard_id}"),
                assignment_epoch,
            )
            .await?
        {
            tracing::info!(
                event_code = "ZONE_STORAGE_SCAN_ASSIGNMENT_LOST",
                shard_id,
                assignment_epoch
            );
            return Ok(None);
        }
        for name in chunk {
            let mut total_size = 0_i64;
            let mut continuation_token = None;
            loop {
                let request = client.list_objects_v2().bucket(name);
                let request = if let Some(token) = continuation_token.as_deref() {
                    request.continuation_token(token)
                } else {
                    request
                };
                let response = request
                    .send()
                    .await
                    .map_err(|error| format!("list objects for {name}: {error}"))?;
                for object in response.contents.unwrap_or_default() {
                    total_size = total_size.saturating_add(object.size.unwrap_or_default());
                }
                if response.is_truncated.unwrap_or(false) {
                    continuation_token = response.next_continuation_token;
                } else {
                    break;
                }
            }
            sizes.push((name.clone(), total_size));
        }
        if !config.storage_scan_batch_pause.is_zero() {
            tokio::time::sleep(config.storage_scan_batch_pause).await;
        }
    }
    if !state
        .assignment_is_current(
            &format!("assignment.storage_scan.{shard_id}"),
            assignment_epoch,
        )
        .await?
    {
        return Ok(None);
    }
    Ok(Some(sizes))
}

fn bucket_shard(name: &str, shard_count: u16) -> u16 {
    let digest = Sha256::digest(name.as_bytes());
    let value = u16::from_be_bytes([digest[0], digest[1]]);
    value % shard_count
}

async fn wait_or_cancel(shutdown: &CancellationToken, duration: Duration) -> bool {
    tokio::select! {
        _ = shutdown.cancelled() => false,
        _ = tokio::time::sleep(duration) => true,
    }
}
