use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;

use crate::config::Config;
use crate::executor::storage::core::client::MinioClient;
use crate::infra::redis::RedisClientManager;
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
        redis_internal_zone: Arc<RedisClientManager>,
        redis_job: Arc<RedisClientManager>,
    ) {
        tokio::spawn(async move {
            Logger::sys_info(
                "storage_syncer.start",
                "StorageSizesSyncer: Khởi chạy luồng nền quét dung lượng bucket mỗi 15s...",
            );

            let mut conn_l2_opt = None;
            let mut conn_job_opt = None;

            loop {
                // [COMMENT]: Chu kỳ 15 giây
                sleep(Duration::from_secs(15)).await;

                // [COMMENT]: Đảm bảo kết nối Redis L2 (internal zone)
                if conn_l2_opt.is_none() {
                    match redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        Ok(conn) => conn_l2_opt = Some(conn),
                        Err(e) => {
                            Logger::sys_error(
                                "storage_syncer.redis_l2_connect_error",
                                "Không thể kết nối tới Redis L2 để đọc metadata",
                                &e.to_string(),
                            );
                            continue;
                        }
                    }
                }

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

                let mut conn_l2 = conn_l2_opt.clone().unwrap();
                let mut conn_job = conn_job_opt.clone().unwrap();

                // [COMMENT]: Kiểm tra trạng thái của Zone và dịch vụ Storage từ Redis L2
                let metadata_res: Result<HashMap<String, String>, redis::RedisError> =
                    redis::cmd("HGETALL")
                        .arg("infra:zone:metadata")
                        .query_async(&mut conn_l2)
                        .await;

                let (zone_active, storage_enabled) = match metadata_res {
                    Ok(metadata) => {
                        let status = metadata
                            .get("status")
                            .cloned()
                            .unwrap_or_else(|| "active".to_string());
                        let storage = metadata
                            .get("service:storage")
                            .cloned()
                            .unwrap_or_else(|| "enabled".to_string());
                        (status == "active", storage == "enabled")
                    }
                    Err(e) => {
                        Logger::sys_warn(
                            "storage_syncer.read_metadata_fail",
                            "Không thể đọc metadata từ Redis L2, mặc định là enabled",
                            &e.to_string(),
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

                // [COMMENT]: Khởi tạo MinIO client (S3 SDK)
                let minio_client = MinioClient::from_env().await;
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

                // [COMMENT]: 4. Gửi kết quả dung lượng quét được lên Redis Stream và báo Pub/Sub
                if !bucket_sizes.is_empty() {
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
                }
            }
        });
    }
}
