use serde::Serialize;
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

use crate::config::Config;
use crate::executor::storage::core::client::MinioClient;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

// [COMMENT]: Định nghĩa cấu trúc tin nhắn gửi lên Redis PubSub
#[derive(Serialize)]
pub struct SyncBucketSizesMsg {
    pub sizes: HashMap<String, i64>,
}

pub struct StorageSizesSyncer;

impl StorageSizesSyncer {
    // [COMMENT]: Khởi chạy luồng giám sát và quét dung lượng bucket định kỳ mỗi 15 giây
    pub fn start(
        _config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        redis_job: Arc<RedisClientManager>,
    ) {
        tokio::spawn(async move {
            Logger::sys_info(
                "storage_syncer.start",
                "StorageSizesSyncer: Khởi chạy luồng nền quét dung lượng bucket mỗi 15s...",
            );

            let mut conn_job_opt = None;
            let instance_id = std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));

            loop {
                // [COMMENT]: Chu kỳ 15 giây
                sleep(Duration::from_secs(15)).await;

                // [COMMENT]: Đảm bảo kết nối Redis Job (Pub/Sub broker)
                if conn_job_opt.is_none() {
                    match redis_job.client().get_multiplexed_tokio_connection().await {
                        Ok(conn) => conn_job_opt = Some(conn),
                        Err(e) => {
                            Logger::sys_error(
                                "storage_syncer.redis_job_connect_error",
                                "Không thể kết nối tới Redis Job để publish dung lượng",
                                &e.to_string(),
                            );
                            continue;
                        }
                    }
                }

                let mut conn_job = conn_job_opt.clone().unwrap();

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

                // [COMMENT]: 4. Gửi kết quả dung lượng quét được lên Redis Stream và báo Pub/Sub
                if !bucket_sizes.is_empty() && may_publish {
                    let msg = SyncBucketSizesMsg {
                        sizes: bucket_sizes,
                    };
                    if let Ok(payload) = serde_json::to_string(&msg) {
                        let stream_key = format!("sizes:{}", _config.zone_id);

                        // [COMMENT]: XADD với MAXLEN ~ 2 để chỉ lưu 2 chu kỳ gần nhất
                        let xadd_res: Result<(), redis::RedisError> = redis::cmd("XADD")
                            .arg(&stream_key)
                            .arg("MAXLEN")
                            .arg("~")
                            .arg("2")
                            .arg("*")
                            .arg("payload")
                            .arg(&payload)
                            .query_async(&mut conn_job)
                            .await;

                        match xadd_res {
                            Ok(_) => {
                                // [COMMENT]: Bắn tín hiệu vào Stream thông báo chung sizes:event-stream
                                let publish_res: Result<(), redis::RedisError> = redis::cmd("XADD")
                                    .arg("sizes:event-stream")
                                    .arg("MAXLEN")
                                    .arg("~")
                                    .arg("1000") // Giới hạn tối đa 1000 bản ghi lịch sử sự kiện
                                    .arg("*")
                                    .arg("zone_id")
                                    .arg(&_config.zone_id)
                                    .query_async(&mut conn_job)
                                    .await;

                                if let Err(e) = publish_res {
                                    Logger::sys_error(
                                        "storage_syncer.event_stream_fail",
                                        "Lỗi khi XADD vào sizes:event-stream",
                                        &e.to_string(),
                                    );
                                    conn_job_opt = None;
                                } else {
                                    Logger::sys_info(
                                        "storage_syncer.sync_success",
                                        &format!("Đã ghi nhận dung lượng và gửi sự kiện lên sizes:event-stream cho zone '{}'.", _config.zone_id),
                                    );
                                }
                            }
                            Err(e) => {
                                Logger::sys_error(
                                    "storage_syncer.xadd_fail",
                                    &format!("Lỗi khi XADD lên Redis Stream '{}'", stream_key),
                                    &e.to_string(),
                                );
                                conn_job_opt = None; // Reset conn
                            }
                        }
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
