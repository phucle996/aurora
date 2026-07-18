use serde::Deserialize;
use std::collections::HashMap;

use super::db;
use crate::config::Config;
use crate::observability::logger::Logger;

// [COMMENT]: Khai báo cấu trúc nhận về từ Redis Stream
#[derive(Deserialize, Debug)]
struct SyncBucketSizesMsg {
    sizes: HashMap<String, i64>,
}

// [COMMENT]: Khởi chạy listener lắng nghe sự kiện cập nhật dung lượng từ Dataplane qua Redis Stream & Consumer Group
pub async fn run_bucket_sizes_listener(
    config: &Config,
    redis_client: &redis::Client,
    nats_client: &async_nats::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    const STREAM_KEY: &str = "sizes:event-stream";
    const GROUP_NAME: &str = "storage-sizes-group";
    const CONSUMER_NAME: &str = "job-orchestrator-consumer";

    Logger::sys_info(
        "storage_listener.run",
        &format!(
            "Storage Bucket Sizes Listener: Khởi tạo Consumer Group '{}' trên Stream '{}'...",
            GROUP_NAME, STREAM_KEY
        ),
    );

    // [COMMENT]: Khởi tạo kết nối Redis L1 multiplexed
    let mut conn = redis_client.get_multiplexed_tokio_connection().await?;

    // [COMMENT]: Tạo Consumer Group (Tự động tạo Stream nếu chưa tồn tại bằng MKSTREAM)
    // Bỏ qua lỗi BUSYGROUP nếu group đã được khởi tạo trước đó bởi replica khác.
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(STREAM_KEY)
        .arg(GROUP_NAME)
        .arg("$")
        .arg("MKSTREAM")
        .query_async(&mut conn)
        .await;

    loop {
        // [COMMENT]: Đọc tin nhắn mới từ stream thông qua Consumer Group (Blocking 2s)
        let reply_res: Result<redis::Value, redis::RedisError> = redis::cmd("XREADGROUP")
            .arg("GROUP")
            .arg(GROUP_NAME)
            .arg(CONSUMER_NAME)
            .arg("BLOCK")
            .arg(2000)
            .arg("COUNT")
            .arg(1) // Xử lý tuần tự từng tin nhắn sự kiện zone
            .arg("STREAMS")
            .arg(STREAM_KEY)
            .arg(">")
            .query_async(&mut conn)
            .await;

        let reply = match reply_res {
            Ok(val) => val,
            Err(e) => {
                Logger::sys_error(
                    "storage_listener.xreadgroup_err",
                    "Lỗi khi đọc sự kiện từ sizes:event-stream",
                    &e.to_string(),
                );
                tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                continue;
            }
        };

        // [COMMENT]: Duyệt qua các bản ghi nhận được từ Stream
        if let redis::Value::Bulk(streams) = reply {
            if let Some(redis::Value::Bulk(stream_data)) = streams.first() {
                if let Some(redis::Value::Bulk(entries)) = stream_data.get(1) {
                    for entry in entries {
                        if let redis::Value::Bulk(entry_fields) = entry {
                            let msg_id = match entry_fields.first() {
                                Some(redis::Value::Data(d)) => {
                                    String::from_utf8_lossy(d).into_owned()
                                }
                                _ => continue,
                            };

                            let fields = match entry_fields.get(1) {
                                Some(redis::Value::Bulk(f)) => f,
                                _ => continue,
                            };

                            let mut zone_id = String::new();
                            for chunk in fields.chunks(2) {
                                if chunk.len() == 2 {
                                    if let (redis::Value::Data(k), redis::Value::Data(v)) =
                                        (&chunk[0], &chunk[1])
                                    {
                                        if String::from_utf8_lossy(k) == "zone_id" {
                                            zone_id = String::from_utf8_lossy(v).into_owned();
                                        }
                                    }
                                }
                            }

                            if zone_id.is_empty() {
                                // [COMMENT]: ACK tin nhắn lỗi/thiếu trường để tránh kẹt hàng đợi
                                let _: redis::RedisResult<()> = redis::cmd("XACK")
                                    .arg(STREAM_KEY)
                                    .arg(GROUP_NAME)
                                    .arg(&msg_id)
                                    .query_async(&mut conn)
                                    .await;
                                continue;
                            }

                            // [COMMENT]: 2. Đọc 2 bản ghi mới nhất từ Stream sizes:<zone_id> của Zone tương ứng
                            let zone_stream_key = format!("sizes:{}", zone_id);
                            let zone_reply_res: Result<redis::Value, redis::RedisError> =
                                redis::cmd("XREVRANGE")
                                    .arg(&zone_stream_key)
                                    .arg("+")
                                    .arg("-")
                                    .arg("COUNT")
                                    .arg(2)
                                    .query_async(&mut conn)
                                    .await;

                            let zone_reply = match zone_reply_res {
                                Ok(val) => val,
                                Err(e) => {
                                    Logger::sys_error(
                                        "storage_listener.xrevrange_err",
                                        &format!("Lỗi khi đọc Stream zone '{}'", zone_stream_key),
                                        &e.to_string(),
                                    );
                                    continue;
                                }
                            };

                            // [COMMENT]: Phân tích raw redis::Value để lấy payloads của zone
                            let mut payloads = Vec::new();
                            if let redis::Value::Bulk(zone_entries) = zone_reply {
                                for z_entry in zone_entries {
                                    if let redis::Value::Bulk(z_entry_fields) = z_entry {
                                        if let Some(redis::Value::Bulk(z_fields)) =
                                            z_entry_fields.get(1)
                                        {
                                            for chunk in z_fields.chunks(2) {
                                                if chunk.len() == 2 {
                                                    if let (
                                                        redis::Value::Data(k),
                                                        redis::Value::Data(v),
                                                    ) = (&chunk[0], &chunk[1])
                                                    {
                                                        if String::from_utf8_lossy(k) == "payload" {
                                                            let val = String::from_utf8_lossy(v)
                                                                .into_owned();
                                                            payloads.push(val);
                                                        }
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }

                            if payloads.is_empty() {
                                // [COMMENT]: Không có dữ liệu, ACK và tiếp tục
                                let _: redis::RedisResult<()> = redis::cmd("XACK")
                                    .arg(STREAM_KEY)
                                    .arg(GROUP_NAME)
                                    .arg(&msg_id)
                                    .query_async(&mut conn)
                                    .await;
                                continue;
                            }

                            // [COMMENT]: Giải mã chu kỳ hiện tại (mới nhất)
                            let current_msg: SyncBucketSizesMsg =
                                match serde_json::from_str(&payloads[0]) {
                                    Ok(data) => data,
                                    Err(e) => {
                                        Logger::sys_error(
                                            "storage_listener.deserialize_current",
                                            "Không thể giải mã payload chu kỳ hiện tại",
                                            &e.to_string(),
                                        );
                                        let _: redis::RedisResult<()> = redis::cmd("XACK")
                                            .arg(STREAM_KEY)
                                            .arg(GROUP_NAME)
                                            .arg(&msg_id)
                                            .query_async(&mut conn)
                                            .await;
                                        continue;
                                    }
                                };

                            // [COMMENT]: Giải mã chu kỳ trước (phần tử thứ 2 nếu có)
                            let old_msg: Option<SyncBucketSizesMsg> = if payloads.len() >= 2 {
                                serde_json::from_str(&payloads[1]).ok()
                            } else {
                                None
                            };

                            // Map gom đổi dung lượng theo user_id: user_id -> HashMap<bucket_name, size>
                            let mut user_changed_buckets: HashMap<String, HashMap<String, i64>> =
                                HashMap::new();

                            // [COMMENT]: 3. So sánh, cập nhật DB và gom thông tin thay đổi theo User ID
                            for (name, size) in current_msg.sizes {
                                let old_size =
                                    old_msg.as_ref().and_then(|m| m.sizes.get(&name)).cloned();

                                // [COMMENT]: Chỉ thực hiện ghi nhận vào DB nếu dung lượng thay đổi so với lần trước
                                let is_changed = old_size.map_or(true, |old| old != size);
                                if is_changed {
                                    let db_url = config.database_url.clone();
                                    let name_clone = name.clone();
                                    let mut target_user_ids = Vec::new();

                                    // Cache size in Redis Hash
                                    let _: redis::RedisResult<()> = redis::cmd("HSET")
                                        .arg("storage:bucket_sizes")
                                        .arg(&name_clone)
                                        .arg(size)
                                        .query_async(&mut conn)
                                        .await;

                                    if name.starts_with("ws-") {
                                        match db::update_personal_bucket_size(
                                            &db_url,
                                            &name_clone,
                                            size,
                                        )
                                        .await
                                        {
                                            Ok(Some(owner_id)) => {
                                                target_user_ids.push(owner_id.clone());
                                                Logger::sys_info(
                                                    "storage_listener.db_write",
                                                    &format!(
                                                        "Đã cập nhật dung lượng bucket cá nhân '{}' lên: {} bytes (User: {})",
                                                        name_clone, size, owner_id
                                                    ),
                                                );

                                                let billing_payload = serde_json::json!({
                                                    "bucket_name": name_clone,
                                                    "owner_id": owner_id,
                                                    "owner_type": "personal",
                                                    "used_bytes": size,
                                                    "timestamp": chrono::Utc::now().timestamp_millis()
                                                });
                                                if let Ok(billing_bin) =
                                                    serde_json::to_vec(&billing_payload)
                                                {
                                                    let _ = nats_client.publish("billing.storage.bucket_used_bytes_update", billing_bin.into()).await;
                                                }
                                            }
                                            Ok(None) => {
                                                Logger::sys_warn(
                                                    "storage_listener.db_write_not_found",
                                                    &format!("Không tìm thấy bucket cá nhân '{}' trong CSDL", name_clone),
                                                    "",
                                                );
                                            }
                                            Err(e) => {
                                                Logger::sys_error(
                                                    "storage_listener.db_write_error",
                                                    &format!(
                                                        "Lỗi ghi nhận CSDL cho bucket cá nhân '{}'",
                                                        name_clone
                                                    ),
                                                    &e.to_string(),
                                                );
                                            }
                                        }
                                    } else if name.starts_with("tn-") {
                                        match db::update_tenant_bucket_size(
                                            &db_url,
                                            &name_clone,
                                            size,
                                        )
                                        .await
                                        {
                                            Ok(user_ids) => {
                                                if !user_ids.is_empty() {
                                                    Logger::sys_info(
                                                        "storage_listener.db_write",
                                                        &format!(
                                                            "Đã cập nhật dung lượng bucket doanh nghiệp '{}' lên: {} bytes ({} active users)",
                                                            name_clone, size, user_ids.len()
                                                        ),
                                                    );
                                                    target_user_ids.extend(user_ids);
                                                } else {
                                                    Logger::sys_warn(
                                                        "storage_listener.db_write_not_found",
                                                        &format!("Không tìm thấy bucket doanh nghiệp '{}' hoặc không có active members", name_clone),
                                                        "",
                                                    );
                                                }
                                            }
                                            Err(e) => {
                                                Logger::sys_error(
                                                    "storage_listener.db_write_error",
                                                    &format!("Lỗi ghi nhận CSDL cho bucket doanh nghiệp '{}'", name_clone),
                                                    &e.to_string(),
                                                );
                                            }
                                        }
                                    }

                                    // [COMMENT]: Kiểm tra nếu chênh lệch dung lượng >= 1MB hoặc là bucket mới thì chuẩn bị bắn lên NATS
                                    let should_publish_nats = old_size
                                        .map_or(size >= 1_048_576, |old| {
                                            (size - old).abs() >= 1_048_576
                                        });
                                    if should_publish_nats {
                                        for user_id in target_user_ids {
                                            user_changed_buckets
                                                .entry(user_id)
                                                .or_insert_with(HashMap::new)
                                                .insert(name.clone(), size);
                                        }
                                    }
                                }
                            }

                            // [COMMENT]: 4. Gửi sự kiện đồng bộ cho từng User bị ảnh hưởng qua NATS Core
                            for (user_id, bucket_sizes_map) in user_changed_buckets {
                                let nats_payload = serde_json::json!({
                                    "sizes": bucket_sizes_map
                                });

                                if let Ok(payload_bin) = serde_json::to_vec(&nats_payload) {
                                    let subject = format!("storage.bucket.sizes.sync.{}", user_id);
                                    match nats_client
                                        .publish(subject.clone(), payload_bin.into())
                                        .await
                                    {
                                        Ok(_) => {
                                            Logger::sys_info(
                                                "storage_listener.nats_publish",
                                                &format!("Đã gửi sự kiện đồng bộ cho user '{}' lên NATS chủ đề '{}'.", user_id, subject),
                                            );
                                        }
                                        Err(e) => {
                                            Logger::sys_error(
                                                "storage_listener.nats_publish_error",
                                                &format!("Không thể gửi thông tin đồng bộ lên NATS cho user '{}'", user_id),
                                                &e.to_string(),
                                            );
                                        }
                                    }
                                }
                            }

                            // [COMMENT]: 5. ACK hoàn thành xử lý tin nhắn
                            let _: redis::RedisResult<()> = redis::cmd("XACK")
                                .arg(STREAM_KEY)
                                .arg(GROUP_NAME)
                                .arg(&msg_id)
                                .query_async(&mut conn)
                                .await;
                        }
                    }
                }
            }
        }
    }
}
