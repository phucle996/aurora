use crate::config::Config;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use prost::Message;
use std::collections::HashMap;
use std::time::{Duration, Instant};

pub mod zone_proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}


/// Khởi chạy vòng lặp lắng nghe báo cáo tài nguyên Zone từ Redis Stream L1 (Platform Level Stream Listener)
/// Task này kiêm luôn vai trò Dead Man's Switch để tự động đánh dấu Zone sập nếu mất kết nối.
pub async fn run_backpressure_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    // 1. Thiết lập kết nối Multiplexed tới Platform Redis (L1)
    let mut conn = redis_client.get_multiplexed_tokio_connection().await?;

    let group_name = "job-proxy-backpressure-group";
    let stream_key = "zone:backpressure:reports";

    // Tự động khởi tạo Consumer Group trên Stream (Durability & HA)
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(stream_key)
        .arg(group_name)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut conn)
        .await;

    let consumer_id = format!("job-proxy-consumer-{}", std::process::id());

    // Cache RAM lưu giữ trạng thái cuối cùng nhận được và timestamp (Dead Man's Switch)
    // Key: zone_id, Value: (last_report_timestamp, current_zone_status, current_mail_enabled)
    let mut zone_heartbeats: HashMap<String, (Instant, String, bool)> = HashMap::new();
    let mut service_metrics_cache: HashMap<(String, String), (String, i32, Instant)> =
        HashMap::new();

    // [COMMENT]: Cache node-level heartbeat cho Dead Man's Switch 45s (Luồng B §5.4 SoT)
    // Key: (zone_id, node_code), Value: (last_seen_instant)
    // Nếu một node vắng mặt khỏi report > 45s -> mark disconnected trong DB
    let mut node_heartbeats: HashMap<(String, String), Instant> = HashMap::new();

    Logger::sys_info(
        "backpressure_listener.run",
        "BackpressureListener: Đang lắng nghe stream 'zone:backpressure:reports'...",
    );

    loop {
        // Thực hiện XREADGROUP chặn 2s (Blocking read 2s to reduce CPU spin)
        let reply_res: Result<redis::Value, redis::RedisError> = redis::cmd("XREADGROUP")
            .arg("GROUP")
            .arg(group_name)
            .arg(&consumer_id)
            .arg("BLOCK")
            .arg(2000)
            .arg("COUNT")
            .arg(10) // Lấy tối đa 10 tin nhắn để xử lý loạt
            .arg("STREAMS")
            .arg(stream_key)
            .arg(">")
            .query_async(&mut conn)
            .await;

        match reply_res {
            Ok(redis::Value::Bulk(streams)) => {
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

                                let fields_val = match entry_fields.get(1) {
                                    Some(redis::Value::Bulk(f)) => f,
                                    _ => continue,
                                };

                                let mut zone_id = String::new();
                                // [COMMENT]: Payload bây giờ là MessagePack binary blob.
                                // Đọc raw bytes từ Redis Data variant, không UTF-8 decode.
                                let mut payload_bytes: Vec<u8> = Vec::new();

                                // Trích xuất các trường từ Stream entry
                                for chunk in fields_val.chunks(2) {
                                    if chunk.len() == 2 {
                                        let k = match &chunk[0] {
                                            redis::Value::Data(d) => String::from_utf8_lossy(d),
                                            _ => continue,
                                        };
                                        if k == "zone_id" {
                                            if let redis::Value::Data(d) = &chunk[1] {
                                                zone_id = String::from_utf8_lossy(d).into_owned();
                                            }
                                        } else if k == "payload" {
                                            if let redis::Value::Data(d) = &chunk[1] {
                                                // Lấy raw bytes, không convert sang String
                                                payload_bytes = d.clone();
                                            }
                                        }
                                    }
                                }

                                if zone_id.is_empty() || payload_bytes.is_empty() {
                                    continue;
                                }

                                // 2. Decode Protobuf binary payload nhận từ Dataplane
                                // [COMMENT]: Giải mã trực tiếp sang struct ZoneReport tự động sinh ra bởi prost.
                                // Loại bỏ toàn bộ chi phí parsing chuỗi JSON/MessagePack và lookup string keys.
                                let payload: zone_proto::ZoneReport =
                                    match zone_proto::ZoneReport::decode(&payload_bytes[..]) {
                                        Ok(v) => v,
                                        Err(e) => {
                                            Logger::sys_error(
                                                "backpressure_listener.decode_error",
                                                "Thất bại khi decode Protobuf payload từ Stream L1",
                                                &e.to_string(),
                                            );
                                            continue;
                                        }
                                    };

                                let cluster = payload.dataplane_cluster.clone().unwrap_or_default();
                                let avg_cpu = cluster.avg_cpu_usage;
                                let avg_ram = cluster.avg_ram_usage;

                                let workloads = payload.workloads.clone().unwrap_or_default();
                                let mail_workload = workloads.mail.clone().unwrap_or_default();
                                let mail_status = &mail_workload.status;
                                let mail_capacity = mail_workload.capacity as usize;

                                // 3. Đo đạc độ ứ đọng hàng đợi hiện tại trong Platform Redis
                                let stream_key_jobs = format!("jobs:{}", zone_id);
                                let queue_len: i64 = redis::cmd("XLEN")
                                    .arg(&stream_key_jobs)
                                    .query_async(&mut conn)
                                    .await
                                    .unwrap_or(0);

                                let pending_res: Result<Vec<redis::Value>, redis::RedisError> =
                                    redis::cmd("XPENDING")
                                        .arg(&stream_key_jobs)
                                        .arg("dataplane_group")
                                        .query_async(&mut conn)
                                        .await;

                                let pending_len: i64 = match pending_res {
                                    Ok(values) => {
                                        if !values.is_empty() {
                                            match &values[0] {
                                                redis::Value::Int(n) => *n,
                                                _ => 0,
                                            }
                                        } else {
                                            0
                                        }
                                    }
                                    Err(_) => 0,
                                };

                                // 4. Truy xuất trạng thái hiện tại từ cache hoặc query DB
                                let (mut current_status, mut current_mail_enabled) =
                                    match zone_heartbeats.get(&zone_id) {
                                        Some((_, status, enabled)) => (status.clone(), *enabled),
                                        None => {
                                            // Khởi động đồng bộ cache từ DB SoT
                                            match super::db::query_current_state(
                                                &config.database_url,
                                                &zone_id,
                                                "mail",
                                            )
                                            .await
                                            {
                                                Ok((status, enabled)) => (status, enabled),
                                                Err(_) => ("active".to_string(), true),
                                            }
                                        }
                                    };

                                // 5. Chạy Decision Engine ra quyết định trạng thái mới của Zone & Service
                                let (target_status, target_mail_enabled) =
                                    super::decision::DecisionEngine::evaluate(
                                        queue_len,
                                        pending_len,
                                        avg_cpu,
                                        avg_ram,
                                        mail_status,
                                        mail_capacity,
                                        &current_status,
                                        current_mail_enabled,
                                    );

                                // 6. Thực hiện cập nhật trực tiếp Postgres DB (Bypass CP gRPC calls)
                                if target_status != current_status {
                                    if let Err(e) = super::db::update_zone_status(
                                        &config.database_url,
                                        &zone_id,
                                        &target_status,
                                    )
                                    .await
                                    {
                                        Logger::sys_error(
                                            "backpressure_listener.db_error",
                                            "Thất bại khi cập nhật trực tiếp status của Zone",
                                            &e.to_string(),
                                        );
                                    } else {
                                        current_status = target_status;
                                    }
                                }

                                if target_mail_enabled != current_mail_enabled {
                                    if let Err(e) = super::db::update_zone_service_status(
                                        &config.database_url,
                                        &zone_id,
                                        "mail",
                                        target_mail_enabled,
                                    )
                                    .await
                                    {
                                        Logger::sys_error(
                                            "backpressure_listener.db_error",
                                            "Thất bại khi cập nhật trực tiếp status của Service",
                                            &e.to_string(),
                                        );
                                    } else {
                                        current_mail_enabled = target_mail_enabled;
                                    }
                                }

                                // 6.5 Cập nhật động status và capacity của service vào Postgres DB (Có Throttling)
                                let now_instant = Instant::now();
                                let mut should_update_metrics = false;
                                if let Some((prev_status, prev_capacity, last_update)) =
                                    service_metrics_cache
                                        .get(&(zone_id.clone(), "mail".to_string()))
                                {
                                    if prev_status != mail_status {
                                        should_update_metrics = true;
                                    } else if (prev_capacity - (mail_capacity as i32)).abs() > 10 {
                                        should_update_metrics = true;
                                    } else if now_instant.duration_since(*last_update)
                                        > Duration::from_secs(120)
                                    {
                                        should_update_metrics = true;
                                    }
                                } else {
                                    should_update_metrics = true;
                                }

                                if should_update_metrics {
                                    if let Err(e) = super::db::update_zone_service_metrics(
                                        &config.database_url,
                                        &zone_id,
                                        "mail",
                                        mail_status,
                                        mail_capacity as i32,
                                    )
                                    .await
                                    {
                                        Logger::sys_error(
                                            "backpressure_listener.metrics_db_error",
                                            "Thất bại khi cập nhật metrics Service vào DB",
                                            &e.to_string(),
                                        );
                                    } else {
                                        service_metrics_cache.insert(
                                            (zone_id.clone(), "mail".to_string()),
                                            (
                                                mail_status.to_string(),
                                                mail_capacity as i32,
                                                now_instant,
                                            ),
                                        );
                                    }
                                }

                                // Ghi nhận heartbeat và ACK bản tin
                                zone_heartbeats.insert(
                                    zone_id.clone(),
                                    (Instant::now(), current_status, current_mail_enabled),
                                );

                                // --- Luồng B: Xử lý workloads.hypervisors[] ---
                                // [COMMENT]: Đọc trực tiếp danh sách hypervisor nodes từ Protobuf struct.
                                // Timestamp của bản tin stream được dùng làm sent_at để race-condition guard.
                                let sent_at = payload.timestamp;

                                for node_proto in &workloads.hypervisors {
                                    let node_code = &node_proto.node_code;
                                    if node_code.is_empty() {
                                        continue; // node_code bắt buộc
                                    }

                                    let node_status = &node_proto.status;
                                    let cpu_cores_total = node_proto.cpu_cores_total;
                                    let cpu_cores_used = node_proto.cpu_cores_used;
                                    let ram_mb_total = node_proto.ram_mb_total;
                                    let ram_mb_used = node_proto.ram_mb_used;
                                    let storage_gb_total = node_proto.storage_gb_total;
                                    let storage_gb_used = node_proto.storage_gb_used;

                                    // [COMMENT]: Upsert vào DB với race-condition guard (last_active_at < sent_at)
                                    // Lỗi upsert được log nhưng không interrupt vòng lặp
                                    if let Err(e) = super::db::upsert_hypervisor_node(
                                        &config.database_url,
                                        &zone_id,
                                        node_code,
                                        node_status,
                                        cpu_cores_total,
                                        cpu_cores_used,
                                        ram_mb_total,
                                        ram_mb_used,
                                        storage_gb_total,
                                        storage_gb_used,
                                        sent_at,
                                    )
                                    .await
                                    {
                                        Logger::sys_error(
                                            "backpressure_listener.hypervisor_upsert",
                                            &format!(
                                                "Lỗi upsert hypervisor node '{}' của Zone {}",
                                                node_code, zone_id
                                            ),
                                            &e.to_string(),
                                        );
                                    }

                                    // Cập nhật node heartbeat cache (Dead Man's Switch node-level)
                                    node_heartbeats.insert(
                                        (zone_id.clone(), node_code.clone()),
                                        Instant::now(),
                                    );
                                }

                                let _: redis::RedisResult<()> = redis::cmd("XACK")
                                    .arg(stream_key)
                                    .arg(group_name)
                                    .arg(&msg_id)
                                    .query_async(&mut conn)
                                    .await;
                            }
                        }
                    }
                }
            }
            _ => {
                // Timeout / Lỗi kết nối tạm thời -> Sẽ được xử lý bởi Dead Man's Switch tiếp sau
            }
        }

        // 7. Cơ chế Dead Man's Switch (Heartbeat timeout check)
        let now_instant = Instant::now();
        let mut zones_to_deactivate = Vec::new();

        for (zone_id, (last_seen, current_status, _)) in zone_heartbeats.iter() {
            // Nếu quá 30 giây không nhận được report của zone active -> Đánh dấu sập (HA safety check)
            if current_status != "inactive"
                && now_instant.duration_since(*last_seen) > Duration::from_secs(30)
            {
                zones_to_deactivate.push(zone_id.clone());
            }
        }

        for zone_id in zones_to_deactivate {
            Logger::sys_warn(
                "backpressure_listener.deadman",
                &format!("Zone {} quá 30 giây không gửi metrics report. Tự động chuyển sang status: 'inactive'.", zone_id),
                "Heartbeat Timeout (Dead Man's Switch Triggered)",
            );

            // Cập nhật DB chuyển zone sang inactive và disabled mail service (Bypass CP)
            let _ = super::db::update_zone_status(&config.database_url, &zone_id, "inactive").await;
            let _ = super::db::update_zone_service_status(
                &config.database_url,
                &zone_id,
                "mail",
                false,
            )
            .await;

            // Cập nhật metrics service sập hoàn toàn
            let _ = super::db::update_zone_service_metrics(
                &config.database_url,
                &zone_id,
                "mail",
                "down",
                0,
            )
            .await;

            // Reset metrics cache
            service_metrics_cache.insert(
                (zone_id.clone(), "mail".to_string()),
                ("down".to_string(), 0, Instant::now()),
            );

            // Đồng bộ cache RAM
            if let Some(val) = zone_heartbeats.get_mut(&zone_id) {
                val.0 = Instant::now(); // Reset timer
                val.1 = "inactive".to_string();
                val.2 = false;
            }
        }

        // 7b. Dead Man's Switch NODE-LEVEL (Luồng B §5.4 SoT: 45 giây node vắng mặt -> disconnected)
        // [COMMENT]: Gom danh sách các node vắng mặt khỏi report quá 45 giây (per zone).
        // Không cần lock vì node_heartbeats chỉ được truy cập bởi 1 tokio task (single-threaded loop).
        let mut nodes_to_disconnect: HashMap<String, Vec<String>> = HashMap::new();
        let node_timeout = Duration::from_secs(45);

        node_heartbeats.retain(|(zone_id, node_code), last_seen| {
            if now_instant.duration_since(*last_seen) > node_timeout {
                // Node vắng mặt > 45s -> đưa vào danh sách cần mark disconnected
                nodes_to_disconnect
                    .entry(zone_id.clone())
                    .or_default()
                    .push(node_code.clone());
                false // Xóa khỏi cache (tránh re-trigger)
            } else {
                true // Giữ lại trong cache
            }
        });

        // Batch mark disconnected theo từng zone (1 DB round-trip mỗi zone)
        for (zone_id, dead_nodes) in nodes_to_disconnect {
            if let Err(e) = super::db::mark_hypervisor_nodes_disconnected(
                &config.database_url,
                &zone_id,
                &dead_nodes,
            )
            .await
            {
                Logger::sys_error(
                    "backpressure_listener.node_deadman",
                    &format!(
                        "Lỗi mark {} node của Zone {} sang disconnected",
                        dead_nodes.len(),
                        zone_id
                    ),
                    &e.to_string(),
                );
            }
        }
    }
}

/// Khởi chạy task PubSub lắng nghe các yêu cầu truy vấn metadata ngược từ Dataplane (Platform Level Metadata Query)
#[allow(deprecated)]
pub async fn run_metadata_query_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let conn = redis_client.get_multiplexed_tokio_connection().await?;
    let conn_async = redis_client.get_async_connection().await?;
    let mut pubsub_conn = conn_async.into_pubsub();

    let channel_name = "zone:query:metadata";
    pubsub_conn.subscribe(channel_name).await?;

    Logger::sys_info(
        "metadata_query_listener.run",
        &format!(
            "MetadataQueryListener: Đang lắng nghe kênh PubSub '{}'...",
            channel_name
        ),
    );

    let mut stream = pubsub_conn.on_message();

    while let Some(msg) = stream.next().await {
        let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
        if payload_bin.is_empty() {
            continue;
        }

        // Parse request JSON thô dạng binary (Avoid UTF-8 String allocations)
        let req_json: serde_json::Value = match serde_json::from_slice(&payload_bin) {
            Ok(v) => v,
            Err(_) => continue,
        };

        let zone_id = match req_json.get("zone_id").and_then(|v| v.as_str()) {
            Some(z) => z.to_string(),
            None => continue,
        };

        let reply_channel = match req_json.get("reply_channel").and_then(|v| v.as_str()) {
            Some(rc) => rc.to_string(),
            None => continue,
        };

        // Query database SoT
        let db_url = config.database_url.clone();
        let zone_id_clone = zone_id.clone();
        let reply_channel_clone = reply_channel.clone();
        let mut conn_clone = conn.clone();

        tokio::spawn(async move {
            match super::db::query_zone_metadata(&db_url, &zone_id_clone)
                .await
                .map_err(|e| e.to_string())
            {
                Ok((status, services)) => {
                    let response = serde_json::json!({
                        "zone_id": zone_id_clone,
                        "status": status,
                        "services": services
                    });

                    // Mã hóa thẳng sang Vec<u8> để truyền tải nhị phân (Network Optimize)
                    if let Ok(response_bin) = serde_json::to_vec(&response) {
                        let publish_res: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
                            .arg(&reply_channel_clone)
                            .arg(&response_bin[..])
                            .query_async(&mut conn_clone)
                            .await;

                        if let Err(e) = publish_res {
                            Logger::sys_error(
                                "metadata_query_listener.publish",
                                "Thất bại khi gửi metadata phản hồi về Dataplane",
                                &e.to_string(),
                            );
                        }
                    }
                }
                Err(e) => {
                    Logger::sys_error(
                        "metadata_query_listener.db_query",
                        &format!("Lỗi khi truy vấn metadata cho Zone {}", zone_id_clone),
                        &e.to_string(),
                    );
                }
            }
        });
    }

    Ok(())
}
