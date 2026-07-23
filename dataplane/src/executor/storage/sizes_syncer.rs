use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

use crate::config::Config;
use crate::executor::storage::core::client::MinioClient;
use crate::infra::kafka::transport_proto::{BucketSizeV1, StorageBucketSizesSnapshotV1};
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

pub struct StorageSizesSyncer;

impl StorageSizesSyncer {
    // [COMMENT]: Khởi chạy luồng giám sát và quét dung lượng bucket định kỳ mỗi 15 giây
    pub fn start(config: Arc<Config>, zone_kv: Arc<ZoneKvStore>, kafka: Arc<KafkaTransport>) {
        tokio::spawn(async move {
            Logger::sys_info(
                "storage_syncer.start",
                "StorageSizesSyncer: Khởi chạy luồng nền quét dung lượng bucket mỗi 15s...",
            );

            let instance_id = std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));

            loop {
                // [COMMENT]: Chu kỳ 15 giây
                sleep(Duration::from_secs(15)).await;

                let (zone_active, storage_enabled) = match zone_kv.read_zone_metadata().await {
                    Ok(metadata) => (
                        metadata.status == "active",
                        metadata.services.get("storage").copied().unwrap_or(true),
                    ),
                    Err(e) => {
                        Logger::sys_warn(
                            "storage_syncer.read_metadata_fail",
                            "Không thể đọc metadata từ Zone KV, mặc định là enabled",
                            &e,
                        );
                        (true, true)
                    }
                };

                // [COMMENT]: Chỉ thực hiện khi Zone active và dịch vụ storage được bật (enabled)
                if !zone_active || !storage_enabled {
                    Logger::sys_debug(
                        "storage_syncer.skip",
                        &format!(
                            "Bỏ qua chu kỳ quét: zone_active={}, storage_enabled={}",
                            zone_active, storage_enabled
                        ),
                    );
                    continue;
                }

                // [COMMENT]: CAS lease trên NATS KV chỉ cho một replica quét MinIO trong chu kỳ.
                // Chỉ cho phép tối đa 1 replica Dataplane thực hiện quét kích thước tệp tin tại 1 chu kỳ (15s),
                // hạn chế trùng lặp ghi nhận và giảm tải lượng query lên MinIO API.
                let lock_key = "lease.storage.sizes_syncer";
                let lease = match zone_kv
                    .acquire_rotating_lease(
                        lock_key,
                        &instance_id,
                        Duration::from_secs(12),
                        Duration::from_secs(20),
                    )
                    .await
                {
                    Ok(Some(lease)) => {
                        Logger::sys_debug(
                            "storage_syncer.lock_acquired",
                            "Đã chiếm thành công khóa phân tán. Bắt đầu tiến hành quét dung lượng..."
                        );
                        lease
                    }
                    Ok(None) => continue,
                    Err(e) => {
                        Logger::sys_warn(
                            "storage_syncer.lock_error",
                            "Gặp lỗi khi lấy khóa NATS KV, bỏ qua chu kỳ để đảm bảo an toàn",
                            &e,
                        );
                        continue;
                    }
                };
                // [COMMENT]: Scan có thể lâu hơn TTL; renew song song và chỉ publish khi owner/fencing vẫn còn hợp lệ.
                let renewal_stop = CancellationToken::new();
                let lease_lost = Arc::new(AtomicBool::new(false));
                let renewal_handle = {
                    let zone_kv = zone_kv.clone();
                    let lease = lease.clone();
                    let renewal_stop = renewal_stop.clone();
                    let lease_lost = lease_lost.clone();
                    tokio::spawn(async move {
                        loop {
                            tokio::select! {
                                _ = renewal_stop.cancelled() => break,
                                _ = sleep(Duration::from_secs(4)) => {
                                    if !zone_kv
                                        .renew_lease(&lease, Duration::from_secs(12))
                                        .await
                                        .unwrap_or(false)
                                    {
                                        lease_lost.store(true, Ordering::Release);
                                        break;
                                    }
                                }
                            }
                        }
                    })
                };

                // [COMMENT]: Khởi tạo MinIO client (S3 SDK)
                let minio_client = MinioClient::from_env_private().await;
                let s3 = minio_client.s3();

                // [COMMENT]: 1. Quét danh sách tất cả các buckets hiện có
                let list_buckets_res = s3.list_buckets().send().await;
                let buckets = match list_buckets_res {
                    Ok(resp) => resp.buckets.unwrap_or_default(),
                    Err(e) => {
                        Logger::sys_error(
                            "storage_syncer.list_buckets_fail",
                            "Không thể lấy danh sách buckets từ MinIO",
                            &e.to_string(),
                        );
                        renewal_stop.cancel();
                        let _ = renewal_handle.await;
                        let _ = zone_kv.release_lease(&lease).await;
                        continue;
                    }
                };

                let mut bucket_sizes = HashMap::new();

                // [COMMENT]: 2. Lặp qua từng bucket, chỉ lọc các bucket của người dùng (ws- hoặc tn-)
                for b in buckets {
                    if let Some(ref name) = b.name {
                        if name.starts_with("ws-") || name.starts_with("tn-") {
                            // [COMMENT]: 3. Duyệt objects của bucket để tính tổng dung lượng thực tế
                            let mut total_size: i64 = 0;
                            let mut continuation_token: Option<String> = None;
                            let mut list_failed = false;

                            loop {
                                let mut req = s3.list_objects_v2().bucket(name);
                                if let Some(ref token) = continuation_token {
                                    req = req.continuation_token(token);
                                }

                                match req.send().await {
                                    Ok(resp) => {
                                        if let Some(contents) = resp.contents {
                                            for obj in contents {
                                                total_size += obj.size.unwrap_or(0);
                                            }
                                        }
                                        if resp.is_truncated.unwrap_or(false) {
                                            continuation_token = resp.next_continuation_token;
                                        } else {
                                            break;
                                        }
                                    }
                                    Err(e) => {
                                        Logger::sys_error(
                                            "storage_syncer.list_objects_fail",
                                            &format!(
                                                "Lỗi khi list objects trong bucket '{}'",
                                                name
                                            ),
                                            &e.to_string(),
                                        );
                                        list_failed = true;
                                        break;
                                    }
                                }
                            }

                            if !list_failed {
                                bucket_sizes.insert(name.clone(), total_size);
                            }
                        }
                    }
                }

                // [COMMENT]: CAS renew ngay trước side effect; owner đã mất lease chỉ được bỏ snapshot, không publish đè chu kỳ mới.
                let may_publish = !lease_lost.load(Ordering::Acquire)
                    && zone_kv
                        .renew_lease(&lease, Duration::from_secs(12))
                        .await
                        .unwrap_or(false);

                // [COMMENT]: Một compact Protobuf snapshot thay hai Redis Stream; key Zone giữ thứ tự theo Zone.
                if !bucket_sizes.is_empty() && may_publish {
                    let event_id = uuid::Uuid::new_v4();
                    let snapshot = StorageBucketSizesSnapshotV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        zone_id: uuid::Uuid::parse_str(&config.zone_id)
                            .map(|value| value.as_bytes().to_vec())
                            .unwrap_or_default(),
                        observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        buckets: bucket_sizes
                            .into_iter()
                            .map(|(bucket_name, size_bytes)| BucketSizeV1 {
                                bucket_name,
                                size_bytes,
                            })
                            .collect(),
                        schema_version: 1,
                    };
                    if let Err(error) = kafka
                        .publish_message(
                            &kafka.storage_sizes_topic(),
                            config.zone_id.as_bytes(),
                            &snapshot,
                        )
                        .await
                    {
                        Logger::sys_error(
                            "storage_syncer.kafka_publish_failed",
                            "Không thể publish storage size snapshot",
                            &error,
                        );
                    }
                } else if !may_publish {
                    Logger::sys_warn(
                        "storage_syncer.lease_lost",
                        "Bỏ snapshot bucket sizes vì replica đã mất fenced lease",
                        "ZONE_KV_LEASE_LOST",
                    );
                }
                renewal_stop.cancel();
                let _ = renewal_handle.await;
                let _ = zone_kv.release_lease(&lease).await;
            }
        });
    }
}
