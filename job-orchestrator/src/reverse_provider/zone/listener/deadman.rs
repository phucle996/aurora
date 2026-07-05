use crate::config::Config;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::time::Instant;
// [COMMENT]: Import hypervisor DB ops từ provider riêng biệt
use super::super::super::hypervisor::db as hypervisor_db;

/// [COMMENT]: Kiểm tra Dead Man's Switch Zone-level (30 giây không nhận heartbeat -> disabled)
/// zone_heartbeats: Key: zone_id, Value: (last_report_ts, zone_status, mail_enabled, storage_enabled)
/// service_metrics_cache: Key: (zone_id, service), Value: (status, capacity, last_update)
pub async fn check_zone_heartbeats(
    config: &Config,
    zone_heartbeats: &mut HashMap<String, (Instant, String, bool, bool)>,
    service_metrics_cache: &mut HashMap<(String, String), (String, i32, Instant)>,
) {
    let now_instant = Instant::now();
    let mut zones_to_deactivate = Vec::new();

    for (zone_id, (last_seen, current_status, _, _)) in zone_heartbeats.iter() {
        // [COMMENT]: Nếu quá 30 giây không nhận được report của zone active -> Đánh dấu sập (HA safety check)
        if current_status != "disabled"
            && now_instant.duration_since(*last_seen) > std::time::Duration::from_secs(30)
        {
            zones_to_deactivate.push(zone_id.clone());
        }
    }

    for zone_id in zones_to_deactivate {
        Logger::sys_warn(
            "backpressure_listener.deadman",
            &format!(
                "Zone {} quá 30 giây không gửi metrics report. Tự động chuyển sang status: 'disabled'.",
                zone_id
            ),
            "Heartbeat Timeout (Dead Man's Switch Triggered)",
        );

        // [COMMENT]: Cập nhật DB chuyển zone sang disabled và disabled mail/storage service (Bypass CP)
        let _ =
            super::super::db::update_zone_status(&config.database_url, &zone_id, "disabled").await;
        let _ = super::super::db::update_zone_service_status(
            &config.database_url,
            &zone_id,
            "mail",
            false,
        )
        .await;
        let _ = super::super::db::update_zone_service_status(
            &config.database_url,
            &zone_id,
            "storage",
            false,
        )
        .await;

        // [COMMENT]: Cập nhật metrics service sập hoàn toàn về down/0 capacity
        let _ = super::super::db::update_zone_service_metrics(
            &config.database_url,
            &zone_id,
            "mail",
            "down",
            0,
        )
        .await;
        let _ = super::super::db::update_zone_service_metrics(
            &config.database_url,
            &zone_id,
            "storage",
            "down",
            0,
        )
        .await;

        // [COMMENT]: Reset metrics cache để tránh stale data khi zone hồi phục
        service_metrics_cache.insert(
            (zone_id.clone(), "mail".to_string()),
            ("down".to_string(), 0, Instant::now()),
        );
        service_metrics_cache.insert(
            (zone_id.clone(), "storage".to_string()),
            ("down".to_string(), 0, Instant::now()),
        );

        // [COMMENT]: Đồng bộ cache RAM: reset timer & tắt toàn bộ service flags
        if let Some(val) = zone_heartbeats.get_mut(&zone_id) {
            val.0 = Instant::now(); // Reset timer để tránh lặp lại Dead Man's Switch
            val.1 = "disabled".to_string();
            val.2 = false;
            val.3 = false;
        }
    }
}

/// [COMMENT]: Kiểm tra Dead Man's Switch Node-level (45 giây node vắng mặt -> disconnected)
/// node_heartbeats: Key: (zone_id, node_code), Value: last_seen_instant
/// Không cần lock vì chỉ được truy cập bởi 1 tokio task (single-threaded loop).
pub async fn check_node_heartbeats(
    config: &Config,
    node_heartbeats: &mut HashMap<(String, String), Instant>,
) {
    let now_instant = Instant::now();
    let node_timeout = std::time::Duration::from_secs(45);

    // [COMMENT]: Gom danh sách các node vắng mặt khỏi report quá 45 giây (per zone).
    // node_heartbeats.retain() xóa luôn các entry timeout khỏi cache để tránh re-trigger.
    let mut nodes_to_disconnect: HashMap<String, Vec<String>> = HashMap::new();

    node_heartbeats.retain(|(zone_id, node_code), last_seen| {
        if now_instant.duration_since(*last_seen) > node_timeout {
            // [COMMENT]: Node vắng mặt > 45s -> đưa vào danh sách cần mark disconnected
            nodes_to_disconnect
                .entry(zone_id.clone())
                .or_default()
                .push(node_code.clone());
            false // Xóa khỏi cache (tránh re-trigger)
        } else {
            true // Giữ lại trong cache
        }
    });

    // [COMMENT]: Batch mark disconnected theo từng zone (1 DB round-trip mỗi zone)
    for (zone_id, dead_nodes) in nodes_to_disconnect {
        if let Err(e) = hypervisor_db::mark_hypervisor_nodes_disconnected(
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
