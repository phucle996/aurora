use crate::config::Config;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::time::Instant;

/// [COMMENT]: Vòng lặp lắng nghe báo cáo tài nguyên Zone từ Redis Stream L1 (Platform Level Stream Listener)
/// Kiêm luôn vai trò kích hoạt Dead Man's Switch ở cuối mỗi chu kỳ vòng lặp.
pub async fn run_backpressure_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: 1. Thiết lập kết nối Multiplexed tới Platform Redis (L1)
    let mut conn = redis_client.get_multiplexed_tokio_connection().await?;

    let group_name = "job-proxy-backpressure-group";
    let stream_key = "zone:backpressure:reports";

    // [COMMENT]: Tự động khởi tạo Consumer Group trên Stream (Durability & HA)
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(stream_key)
        .arg(group_name)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut conn)
        .await;

    let consumer_id = format!("job-proxy-consumer-{}", std::process::id());

    // [COMMENT]: Cache RAM lưu giữ trạng thái cuối cùng nhận được và timestamp (Dead Man's Switch)
    // Key: zone_id, Value: (last_report_ts, zone_status, mail_enabled, storage_enabled)
    // Đây cũng đóng vai trò EnabledServicesMap — SOT điều phối health check và Decision Engine.
    let mut zone_heartbeats: HashMap<String, (Instant, String, bool, bool)> = HashMap::new();

    // [COMMENT]: Throttle cache cho metrics update - tránh spam DB khi capacity dao động nhỏ
    // Key: (zone_id, service), Value: (status, capacity, last_update_ts)
    let mut service_metrics_cache: HashMap<(String, String), (String, i32, Instant)> =
        HashMap::new();

    // [COMMENT]: Cache node-level heartbeat cho Dead Man's Switch 45s (Luồng B §5.4 SoT)
    // Key: (zone_id, node_code), Value: (last_seen_instant)
    let mut node_heartbeats: HashMap<(String, String), Instant> = HashMap::new();

    // [COMMENT]: BOOTSTRAP SNAPSHOT — Load toàn bộ zone_services từ DB trước khi bắt đầu listen.
    // Đảm bảo EnabledServicesMap (zone_heartbeats) không bị trống sau JO restart,
    // tránh khoảng trống giữa restart và khi nhận được CDC event đầu tiên.
    // HA Guard #16: Snapshot Pattern ngăn Bootstrap Gap race condition.
    match super::super::db::query_all_zone_services_enabled(&config.database_url).await {
        Ok(snapshot) => {
            for (zone_id, services) in &snapshot {
                let mail_en = services.get("mail").copied().unwrap_or(false);
                let storage_en = services.get("storage").copied().unwrap_or(false);

                // [COMMENT]: Lấy zone_status từ DB để khởi tạo đầy đủ heartbeat entry.
                // Nếu lỗi → mặc định "active" để tránh Dead Man's Switch kích hoạt nhầm ngay lúc boot.
                let (zone_status, _) =
                    super::super::db::query_current_state(&config.database_url, zone_id, "mail")
                        .await
                        .unwrap_or_else(|_| ("active".to_string(), false));

                zone_heartbeats.insert(
                    zone_id.clone(),
                    (Instant::now(), zone_status, mail_en, storage_en),
                );
            }

            Logger::sys_info(
                "backpressure_listener.bootstrap",
                &format!(
                    "Bootstrap hoàn tất: {} zones đã được pre-load vào EnabledServicesMap.",
                    snapshot.len()
                ),
            );
        }
        Err(e) => {
            // [COMMENT]: Bootstrap thất bại không critical — zone_heartbeats sẽ được populate
            // dần qua fallback DB read trong processor.rs khi các zone gửi report đầu tiên.
            Logger::sys_error(
                "backpressure_listener.bootstrap_error",
                "Bootstrap zone_services snapshot thất bại — sẽ fallback qua per-zone DB read",
                &e.to_string(),
            );
        }
    }

    Logger::sys_info(
        "backpressure_listener.run",
        "BackpressureListener: Đang lắng nghe stream 'zone:backpressure:reports'...",
    );

    loop {
        // [COMMENT]: Thực hiện XREADGROUP chặn 2s (Blocking read 2s to reduce CPU spin)
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
                                // [COMMENT]: Trích xuất msg_id và các trường từ Stream entry
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
                                // [COMMENT]: Payload là Protobuf binary blob - Đọc raw bytes từ Redis
                                // Data variant, không UTF-8 decode để tránh mất dữ liệu nhị phân.
                                let mut payload_bytes: Vec<u8> = Vec::new();

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
                                                payload_bytes = d.clone();
                                            }
                                        }
                                    }
                                }

                                if zone_id.is_empty() || payload_bytes.is_empty() {
                                    continue;
                                }

                                // [COMMENT]: Ủy thác toàn bộ nghiệp vụ xử lý sang processor module
                                super::processor::process_report(
                                    config,
                                    &mut conn,
                                    zone_id,
                                    payload_bytes,
                                    &mut zone_heartbeats,
                                    &mut service_metrics_cache,
                                    &mut node_heartbeats,
                                )
                                .await;

                                // [COMMENT]: ACK bản tin sau khi xử lý thành công
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
                // [COMMENT]: Timeout / Lỗi kết nối tạm thời -> Sẽ được xử lý bởi Dead Man's Switch tiếp sau
            }
        }

        // [COMMENT]: Dead Man's Switch - Kích hoạt cuối mỗi chu kỳ vòng lặp để kiểm tra heartbeat timeout
        super::deadman::check_zone_heartbeats(
            config,
            &mut zone_heartbeats,
            &mut service_metrics_cache,
        )
        .await;

        super::deadman::check_node_heartbeats(config, &mut node_heartbeats).await;
    }
}
