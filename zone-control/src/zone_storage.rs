use aws_config::BehaviorVersion;
use aws_credential_types::Credentials;
use aws_sdk_s3::config::{Builder, Region};
use aws_sdk_s3::Client as S3Client;
use prost::Message;
use std::collections::HashMap;
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
) -> Result<(), String> {
    const ASSIGNMENT_KEY: &str = "assignment.storage_scan.0";
    let zone_id = Uuid::parse_str(&config.zone_id)
        .map_err(|_| "ZONE_ID is invalid for storage scan".to_string())?;
    loop {
        if !wait_or_cancel(&shutdown, Duration::from_secs(15)).await {
            return Ok(());
        }
        let metadata = state.read_metadata().await?;
        if metadata.status != "active"
            || !metadata.services.get("storage").copied().unwrap_or(false)
        {
            continue;
        }
        let buckets = scan_all_customer_storage_bucket_sizes(&config).await?;
        if buckets.is_empty() {
            continue;
        }
        let snapshot = StorageBucketSizesSnapshotV1 {
            event_id: Uuid::new_v4().as_bytes().to_vec(),
            zone_id: zone_id.as_bytes().to_vec(),
            observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
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
            .assignment_is_current(ASSIGNMENT_KEY, assignment_epoch)
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

async fn scan_all_customer_storage_bucket_sizes(
    config: &Config,
) -> Result<HashMap<String, i64>, String> {
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
    let mut sizes = HashMap::new();
    for bucket in response.buckets.unwrap_or_default() {
        let Some(name) = bucket.name else {
            continue;
        };
        if !name.starts_with("ws-") && !name.starts_with("tn-") {
            continue;
        }
        let mut total_size = 0_i64;
        let mut continuation_token = None;
        loop {
            let request = client.list_objects_v2().bucket(&name);
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
        sizes.insert(name, total_size);
    }
    Ok(sizes)
}

async fn wait_or_cancel(shutdown: &CancellationToken, duration: Duration) -> bool {
    tokio::select! {
        _ = shutdown.cancelled() => false,
        _ = tokio::time::sleep(duration) => true,
    }
}
