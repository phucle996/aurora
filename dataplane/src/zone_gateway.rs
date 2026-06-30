use futures_util::StreamExt;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use redis;

/// Cổng thông tin và Đồng bộ hóa Trạng thái Zone (ZoneStatusGateway)
/// Nhiệm vụ:
///   - Tổng hợp dữ liệu các Node đang sống của cụm (ResourceMonitor) và các Workload (infra:mail).
///   - Tính toán chỉ số tải trung bình và capacity của cả zone.
///   - Đẩy báo cáo tổng hợp lên Redis Platform (L1) qua stream `zone:backpressure:reports`.
///   - Đồng bộ metadata của Zone (Status & Services) từ Platform L1 về Redis L2 (Reverse Metadata Query Flow).
///   - Triển khai Distributed Lock và CDC Real-time Listener.
pub struct ZoneStatusGateway;

impl ZoneStatusGateway {
    /// Khởi chạy task tổng hợp tài nguyên của cả cụm Dataplane và đẩy lên Platform Redis L1 (HA & Self-Healing Sync)
    pub fn start_zone_gateway(
        redis_internal_zone: Arc<RedisClientManager>,
        redis_job: Arc<RedisClientManager>,
        config: Arc<Config>,
    ) {
        tokio::spawn(async move {
            Logger::sys_info(
                "zone_gateway.start",
                "ZoneStatusGateway: Bắt đầu luồng ngầm đồng bộ trạng thái L2 lên Platform L1...",
            );

            // Duy trì kết nối multiplexed tới cả Redis L2 và Redis L1
            let mut conn_l2_opt = None;
            let mut conn_l1_opt = None;

            // Bộ đếm chu kỳ để chạy đồng bộ metadata (Reconciliation).
            // Chu kỳ Polling giãn cách lên 30 phút (360 chu kỳ * 5 giây) để tránh spam, đồng thời làm lưới an toàn (fallback) nếu rớt gói CDC.
            // Khởi tạo bằng 360 để chạy ngay lập tức khi cold start.
            let mut counter = 720;

            loop {
                // Định kỳ đồng bộ metadata của Zone (Status & Services) từ Platform L1 về L2 (30 phút/lần)
                if counter >= 720 {
                    counter = 0;
                    let redis_internal_zone_md = redis_internal_zone.clone();
                    let redis_job_md = redis_job.clone();
                    let config_md = config.clone();
                    tokio::spawn(async move {
                        if let Err(e) = Self::sync_zone_metadata(
                            redis_internal_zone_md,
                            redis_job_md,
                            config_md,
                        )
                        .await
                        {
                            Logger::sys_error(
                                "zone_gateway.sync_metadata_error",
                                "Thất bại khi đồng bộ metadata từ Platform L1 về Redis L2 cục bộ",
                                &e.to_string(),
                            );
                        }
                    });
                }

                // Đảm bảo có kết nối tới cả Redis L2 (Zone) và Redis L1 (Platform)
                if conn_l2_opt.is_none() {
                    if let Ok(conn) = redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        conn_l2_opt = Some(conn);
                    }
                }
                if conn_l1_opt.is_none() {
                    if let Ok(conn) = redis_job.client().get_multiplexed_tokio_connection().await {
                        conn_l1_opt = Some(conn);
                    }
                }

                if let (Some(mut conn_l2), Some(mut conn_l1)) =
                    (conn_l2_opt.clone(), conn_l1_opt.clone())
                {
                    // 1. Quét danh sách các node dataplane đang hoạt động
                    let keys: Vec<String> = redis::cmd("KEYS")
                        .arg("dataplane:node:*")
                        .query_async(&mut conn_l2)
                        .await
                        .unwrap_or_default();

                    let mut total_cpu = 0.0;
                    let mut total_ram = 0.0;
                    let mut total_active_workers = 0;
                    let mut alive_nodes_count = 0;
                    let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
                        Ok(dur) => dur.as_secs(),
                        Err(_) => 0,
                    };

                    for key in keys {
                        let data: std::collections::HashMap<String, String> = redis::cmd("HGETALL")
                            .arg(&key)
                            .query_async(&mut conn_l2)
                            .await
                            .unwrap_or_default();

                        if let (
                            Some(updated_at_str),
                            Some(cpu_str),
                            Some(ram_str),
                            Some(workers_str),
                        ) = (
                            data.get("updated_at"),
                            data.get("cpu"),
                            data.get("ram"),
                            data.get("active_workers"),
                        ) {
                            let updated_at: u64 = updated_at_str.parse().unwrap_or(0);

                            if now.saturating_sub(updated_at) <= 15 {
                                let cpu: f64 = cpu_str.parse().unwrap_or(0.0);
                                let ram: f64 = ram_str.parse().unwrap_or(0.0);
                                let workers: usize = workers_str.parse().unwrap_or(0);

                                total_cpu += cpu;
                                total_ram += ram;
                                total_active_workers += workers;
                                alive_nodes_count += 1;
                            }
                        }
                    }

                    let avg_cpu = if alive_nodes_count > 0 {
                        total_cpu / alive_nodes_count as f64
                    } else {
                        0.0
                    };
                    let avg_ram = if alive_nodes_count > 0 {
                        total_ram / alive_nodes_count as f64
                    } else {
                        0.0
                    };

                    // 2. Đọc trạng thái mail workload từ Redis L2 (infra:mail)
                    let mail_data: std::collections::HashMap<String, String> =
                        redis::cmd("HGETALL")
                            .arg("infra:mail")
                            .query_async(&mut conn_l2)
                            .await
                            .unwrap_or_default();

                    let mail_status = mail_data
                        .get("status")
                        .cloned()
                        .unwrap_or_else(|| "down".to_string());
                    let mail_capacity: usize = mail_data
                        .get("capacity")
                        .and_then(|c| c.parse().ok())
                        .unwrap_or(0);

                    // 3. Đóng gói payload JSON của Zone
                    let payload = serde_json::json!({
                        "zone_id": config.zone_id,
                        "timestamp": now,
                        "dataplane_cluster": {
                            "active_nodes": alive_nodes_count,
                            "avg_cpu_usage": avg_cpu,
                            "avg_ram_usage": avg_ram,
                            "total_active_workers": total_active_workers,
                            "total_max_workers": config.max_workers * alive_nodes_count.max(1)
                        },
                        "workloads": {
                            "mail": {
                                "status": mail_status,
                                "capacity": mail_capacity
                            }
                        }
                    });

                    // 4. Bắn báo cáo lên Platform Redis L1
                    let payload_str = payload.to_string();
                    let xadd_res = tokio::time::timeout(
                        Duration::from_secs(2),
                        redis::cmd("XADD")
                            .arg("zone:backpressure:reports")
                            .arg("MAXLEN")
                            .arg("~")
                            .arg("1000")
                            .arg("*")
                            .arg("zone_id")
                            .arg(&config.zone_id)
                            .arg("payload")
                            .arg(&payload_str)
                            .query_async::<_, ()>(&mut conn_l1),
                    )
                    .await;

                    match xadd_res {
                        Ok(Ok(())) => {
                            Logger::sys_debug(
                                "zone_gateway.reports_pushed",
                                &format!(
                                    "Đã đẩy báo cáo backpressure của Zone {} (nodes: {}, cpu: {}%, ram: {}%)",
                                    config.zone_id, alive_nodes_count, avg_cpu, avg_ram
                                ),
                            );
                        }
                        Ok(Err(e)) => {
                            Logger::sys_error(
                                "zone_gateway.reports_error",
                                "Không thể gửi báo cáo backpressure lên L1",
                                &e.to_string(),
                            );
                            conn_l1_opt = None;
                        }
                        Err(_) => {
                            Logger::sys_error(
                                "zone_gateway.reports_timeout",
                                "Timeout 2s khi gửi báo cáo lên L1",
                                "Timeout",
                            );
                            conn_l1_opt = None;
                        }
                    }
                }

                sleep(Duration::from_secs(5)).await;
                counter += 1;
            }
        });
    }

    /// Lắng nghe các sự kiện cập nhật cấu hình thời gian thực (CDC events) từ Platform L1
    #[allow(deprecated)]
    pub fn start_metadata_event_listener(
        redis_internal_zone: Arc<RedisClientManager>,
        redis_job: Arc<RedisClientManager>,
        config: Arc<Config>,
    ) {
        tokio::spawn(async move {
            let channel_name = format!("zone:event:metadata:{}", config.zone_id);
            Logger::sys_info(
                "zone_gateway.cdc_listener",
                &format!(
                    "Bắt đầu lắng nghe sự kiện CDC Metadata thời gian thực trên kênh {}",
                    channel_name
                ),
            );

            loop {
                // Tự động hồi phục kết nối nếu PubSub bị đứt
                let conn_res = redis_job.client().get_async_connection().await;
                match conn_res {
                    Ok(conn) => {
                        #[allow(deprecated)]
                        let mut pubsub = conn.into_pubsub();
                        if let Err(e) = pubsub.subscribe(&channel_name).await {
                            Logger::sys_error(
                                "zone_gateway.cdc_listener_error",
                                "Không thể subscribe kênh sự kiện CDC",
                                &e.to_string(),
                            );
                            sleep(Duration::from_secs(5)).await;
                            continue;
                        }

                        let mut stream = pubsub.on_message();
                        let mut conn_l2_opt = None;

                        while let Some(msg) = stream.next().await {
                            // Đảm bảo có kết nối Redis L2 để ghi nhận
                            if conn_l2_opt.is_none() {
                                if let Ok(conn) = redis_internal_zone
                                    .client()
                                    .get_multiplexed_tokio_connection()
                                    .await
                                {
                                    conn_l2_opt = Some(conn);
                                }
                            }

                            if let Some(mut conn_l2) = conn_l2_opt.clone() {
                                let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
                                if let Ok(event_json) =
                                    serde_json::from_slice::<serde_json::Value>(&payload_bin)
                                {
                                    let event_type = event_json
                                        .get("event_type")
                                        .and_then(|v| v.as_str())
                                        .unwrap_or("");

                                    match event_type {
                                        "zone_status_changed" => {
                                            if let Some(status) =
                                                event_json.get("status").and_then(|v| v.as_str())
                                            {
                                                let _: Result<(), redis::RedisError> =
                                                    redis::cmd("HSET")
                                                        .arg("infra:zone:metadata")
                                                        .arg("status")
                                                        .arg(status)
                                                        .query_async(&mut conn_l2)
                                                        .await;

                                                Logger::sys_info(
                                                    "zone_gateway.cdc_event",
                                                    &format!("[CDC EVENT] Đã cập nhật trạng thái Zone sang: '{}'", status),
                                                );
                                            }
                                        }
                                        "service_status_changed" => {
                                            let service = event_json
                                                .get("service")
                                                .and_then(|v| v.as_str())
                                                .unwrap_or("");
                                            let enabled = event_json
                                                .get("enabled")
                                                .and_then(|v| v.as_bool())
                                                .unwrap_or(false);

                                            if !service.is_empty() {
                                                let field_name = format!("service:{}", service);
                                                let val_str =
                                                    if enabled { "enabled" } else { "disabled" };
                                                let _: Result<(), redis::RedisError> =
                                                    redis::cmd("HSET")
                                                        .arg("infra:zone:metadata")
                                                        .arg(&field_name)
                                                        .arg(val_str)
                                                        .query_async(&mut conn_l2)
                                                        .await;

                                                Logger::sys_info(
                                                    "zone_gateway.cdc_event",
                                                    &format!("[CDC EVENT] Đã cập nhật trạng thái dịch vụ '{}' sang: '{}'", service, val_str),
                                                );
                                            }
                                        }
                                        _ => {}
                                    }
                                }
                            }
                        }
                    }
                    Err(e) => {
                        Logger::sys_error(
                            "zone_gateway.cdc_listener_reconnect",
                            "Mất kết nối tới Platform L1 Redis. Thử kết nối lại sau 5s...",
                            &e.to_string(),
                        );
                        sleep(Duration::from_secs(5)).await;
                    }
                }
            }
        });
    }

    /// Đồng bộ metadata của Zone (Status & Services) từ Platform L1 về Redis L2 cục bộ (Reverse Metadata Query Flow)
    #[allow(deprecated)]
    pub async fn sync_zone_metadata(
        redis_internal_zone: Arc<RedisClientManager>,
        redis_job: Arc<RedisClientManager>,
        config: Arc<Config>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut conn_l2 = redis_internal_zone
            .client()
            .get_multiplexed_tokio_connection()
            .await?;

        // 0. Cố gắng lấy Distributed Lock trên Redis L2 để tránh Write Stampede/Double-query
        let node_id = uuid::Uuid::new_v4().to_string();
        let lock_key = "lock:zone:sync_metadata";

        let acquired: Result<Option<String>, redis::RedisError> = redis::cmd("SET")
            .arg(lock_key)
            .arg(&node_id)
            .arg("NX")
            .arg("EX")
            .arg(10) // Lock TTL 10 giây
            .query_async(&mut conn_l2)
            .await;

        let has_lock = match acquired {
            Ok(Some(s)) if s == "OK" => true,
            _ => false,
        };

        if !has_lock {
            // Thất bại trong việc lấy lock -> Đã có node khác đang chạy sync
            Logger::sys_debug(
                "zone_gateway.sync_metadata",
                "Không thể lấy lock đồng bộ (đã có node khác trong cụm đang chạy). Bỏ qua chu kỳ sync metadata.",
            );
            return Ok(());
        }

        Logger::sys_info(
            "zone_gateway.sync_metadata",
            "Đã lấy thành công lock đồng bộ metadata. Tiến hành sync từ Platform L1...",
        );

        let mut conn_l1 = redis_job
            .client()
            .get_multiplexed_tokio_connection()
            .await?;
        let conn_l1_async = redis_job.client().get_async_connection().await?;
        let mut pubsub_conn = conn_l1_async.into_pubsub();

        let request_uuid = uuid::Uuid::new_v4().to_string();
        let reply_channel = format!("zone:reply:metadata:{}:{}", config.zone_id, request_uuid);

        // 1. Subscribe kênh reply nhận kết quả phản hồi từ job-proxy
        pubsub_conn.subscribe(&reply_channel).await?;

        let wait_res = {
            let mut stream = pubsub_conn.on_message();

            // 2. Publish request hỏi metadata lên kênh Platform dưới dạng nhị phân thô
            let req_payload = serde_json::json!({
                "zone_id": config.zone_id,
                "reply_channel": reply_channel
            });

            let req_bin = serde_json::to_vec(&req_payload).unwrap_or_default();

            let _: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
                .arg("zone:query:metadata")
                .arg(&req_bin[..])
                .query_async(&mut conn_l1)
                .await;

            // 3. Đợi nhận phản hồi trong timeout 5 giây (HA Safety & Non-blocking fallback)
            tokio::time::timeout(Duration::from_secs(5), stream.next()).await
        };

        // Luôn luôn hủy subscribe kênh reply sau khi xong để tránh rò rỉ bộ nhớ
        let _ = pubsub_conn.unsubscribe(&reply_channel).await;

        match wait_res {
            Ok(Some(msg)) => {
                let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
                let resp_json: serde_json::Value = serde_json::from_slice(&payload_bin)?;

                let status = resp_json
                    .get("status")
                    .and_then(|v| v.as_str())
                    .unwrap_or("inactive");
                let services = resp_json.get("services").and_then(|v| v.as_object());

                let mut pipe = redis::pipe();
                pipe.cmd("HSET")
                    .arg("infra:zone:metadata")
                    .arg("status")
                    .arg(status);

                if let Some(svcs) = services {
                    for (svc_name, enabled_val) in svcs {
                        let val_str = if enabled_val.as_bool().unwrap_or(false) {
                            "enabled"
                        } else {
                            "disabled"
                        };
                        pipe.cmd("HSET")
                            .arg("infra:zone:metadata")
                            .arg(format!("service:{}", svc_name))
                            .arg(val_str);
                    }
                }

                pipe.cmd("HSET")
                    .arg("infra:zone:metadata")
                    .arg("updated_at")
                    .arg(
                        SystemTime::now()
                            .duration_since(UNIX_EPOCH)
                            .unwrap_or_default()
                            .as_secs(),
                    );

                let _: () = pipe.query_async(&mut conn_l2).await?;

                Logger::sys_info(
                    "zone_gateway.sync_metadata",
                    &format!(
                        "Đã đồng bộ metadata Zone {} (Status: {}) về Redis L2 thành công.",
                        config.zone_id, status
                    ),
                );
            }
            _ => {
                Logger::sys_error(
                    "zone_gateway.sync_metadata_timeout",
                    "Quá hạn 5 giây không nhận được metadata phản hồi từ Platform",
                    "Timeout or Empty response",
                );
            }
        }

        // 4. Giải phóng Distributed Lock nguyên tử sử dụng Lua script (HA Safety)
        let lua_script = "
            if redis.call('get', KEYS[1]) == ARGV[1] then
                return redis.call('del', KEYS[1])
            else
                return 0
            end
        ";
        let _: Result<i32, redis::RedisError> = redis::cmd("EVAL")
            .arg(lua_script)
            .arg(1)
            .arg(lock_key)
            .arg(&node_id)
            .query_async(&mut conn_l2)
            .await;

        Ok(())
    }
}
