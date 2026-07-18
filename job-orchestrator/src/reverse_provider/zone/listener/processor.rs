use crate::config::Config;
use crate::observability::logger::Logger;
use prost::Message;
use std::collections::HashMap;
use std::time::{Duration, Instant};

use super::zone_proto;
// [COMMENT]: Import cross-cutting coordinator từ tầng reverse_provider
use super::super::super::decision::DecisionEngine;
// [COMMENT]: Import hypervisor DB ops từ provider riêng biệt
use super::super::super::hypervisor::db as hypervisor_db;

/// [COMMENT]: Xử lý nghiệp vụ chính cho một ZoneReport nhận từ Redis Stream L1.
/// Bao gồm: decode Protobuf, đo queue, đồng bộ cache từ DB, chạy Decision Engine,
/// ghi DB theo kết quả, cập nhật throttle metrics, upsert hypervisor nodes.
///
/// NGUYÊN TẮC ENABLED-ONLY: DecisionEngine chỉ nhận enabled_services từ zone_heartbeats cache.
/// Service disabled không tham gia vào bất kỳ quyết định trạng thái nào.
///
/// Trả về void — toàn bộ side effects ghi vào zone_heartbeats và DB.
pub async fn process_report(
    config: &Config,
    conn: &mut redis::aio::MultiplexedConnection,
    zone_id: String,
    payload_bytes: Vec<u8>,
    zone_heartbeats: &mut HashMap<String, (Instant, String, bool, bool)>,
    service_metrics_cache: &mut HashMap<(String, String), (String, i32, Instant)>,
    node_heartbeats: &mut HashMap<(String, String), Instant>,
) {
    // [COMMENT]: 1. Decode Protobuf binary payload nhận từ Dataplane
    // Giải mã trực tiếp sang struct ZoneReport tự động sinh ra bởi prost.
    let payload: zone_proto::ZoneReport = match zone_proto::ZoneReport::decode(&payload_bytes[..]) {
        Ok(v) => v,
        Err(e) => {
            Logger::sys_error(
                "backpressure_listener.decode_error",
                "Thất bại khi decode Protobuf payload từ Stream L1",
                &e.to_string(),
            );
            return;
        }
    };

    let cluster = payload.dataplane_cluster.clone().unwrap_or_default();
    let avg_cpu = cluster.avg_cpu_usage;
    let avg_ram = cluster.avg_ram_usage;

    let workloads = payload.workloads.clone().unwrap_or_default();
    let mail_workload = workloads.mail.clone().unwrap_or_default();
    let mail_status = mail_workload.status.clone();
    let mail_capacity = mail_workload.capacity as usize;

    // [COMMENT]: Giải mã thông tin storage workload từ Protobuf
    let storage_workload = workloads.storage.clone().unwrap_or_default();
    let storage_status = storage_workload.status.clone();
    let storage_capacity = storage_workload.capacity as usize;

    // [COMMENT]: 2. Đo đạc độ ứ đọng hàng đợi hiện tại trong Platform Redis
    let stream_key_jobs = format!("jobs:{}", zone_id);
    let queue_len: i64 = redis::cmd("XLEN")
        .arg(&stream_key_jobs)
        .query_async(conn)
        .await
        .unwrap_or(0);

    let pending_res: Result<Vec<redis::Value>, redis::RedisError> = redis::cmd("XPENDING")
        .arg(&stream_key_jobs)
        .arg("dataplane_group")
        .query_async(conn)
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

    // [COMMENT]: 3. Truy xuất trạng thái hiện tại từ cache hoặc query DB SoT (Cold Start / Fallback).
    // zone_heartbeats cache đóng vai trò EnabledServicesMap in-memory — giữ cả mail_enabled lẫn storage_enabled.
    // Nếu không có entry (zone mới xuất hiện sau JO boot) → fallback đọc DB, nạp vào cache.
    let (mut current_status, mut current_mail_enabled, mut current_storage_enabled) =
        match zone_heartbeats.get(&zone_id) {
            Some((_, status, mail_en, storage_en)) => (status.clone(), *mail_en, *storage_en),
            None => {
                // [COMMENT]: Fallback: zone chưa có trong RAM cache (zone mới tạo sau bootstrap).
                // Đọc trực tiếp từ DB để lấy zone_status và desired_state của tất cả service.
                let fallback_services = match super::super::db::query_zone_services_enabled(
                    &config.database_url,
                    &zone_id,
                )
                .await
                {
                    Ok(v) => v,
                    Err(e) => {
                        Logger::sys_error(
                            "backpressure_listener.fallback_db_error",
                            &format!(
                                "Không thể fallback đọc zone_services từ DB cho zone {}",
                                zone_id
                            ),
                            &e.to_string(),
                        );
                        // [COMMENT]: Nếu DB lỗi, mặc định coi tất cả disabled để tránh false draining trigger
                        std::collections::HashMap::new()
                    }
                };

                // [COMMENT]: Lấy zone status từ DB thông qua query_current_state (lấy mail làm anchor zone status)
                let (status, _) = match super::super::db::query_current_state(
                    &config.database_url,
                    &zone_id,
                    "mail",
                )
                .await
                {
                    Ok(v) => v,
                    Err(_) => ("active".to_string(), false),
                };

                let mail_en = fallback_services.get("mail").copied().unwrap_or(false);
                let storage_en = fallback_services.get("storage").copied().unwrap_or(false);

                Logger::sys_info(
                    "backpressure_listener.fallback_load",
                    &format!(
                        "Fallback load zone {} từ DB: status={}, mail_enabled={}, storage_enabled={}",
                        zone_id, status, mail_en, storage_en
                    ),
                );

                (status, mail_en, storage_en)
            }
        };

    // [COMMENT]: 4. Chạy Decision Engine — enabled-only evaluation.
    // Chỉ pass vào enabled services. DecisionEngine chỉ trả về target_zone_status.
    // Decision Engine KHÔNG tự toggle desired_state của service — đó là quyền của SRE.
    let target_status = DecisionEngine::evaluate(
        queue_len,
        pending_len,
        avg_cpu,
        avg_ram,
        &mail_status,
        mail_capacity,
        current_mail_enabled,
        &storage_status,
        storage_capacity,
        current_storage_enabled,
        &current_status,
    );

    // [COMMENT]: 5. Thực hiện cập nhật trực tiếp Postgres DB nếu zone_status thay đổi.
    // Chuyển Error sang String để vượt ranh giới async Send trait của Rust.
    if target_status != current_status {
        let update_result =
            super::super::db::update_zone_status(&config.database_url, &zone_id, &target_status)
                .await
                .map_err(|e| e.to_string());

        match update_result {
            Ok(true) => {
                // [COMMENT]: Cập nhật DB thành công, đồng bộ trạng thái mới vào cache.
                current_status = target_status;
            }
            Ok(false) => {
                // [COMMENT]: DB Guard từ chối do vi phạm chuyển dịch trạng thái.
                // Lập tức query lại DB để sửa sai cache RAM (Self-Correcting Cache).
                let corrected =
                    super::super::db::query_zone_services_enabled(&config.database_url, &zone_id)
                        .await;

                if let Ok(services) = corrected {
                    current_mail_enabled = services
                        .get("mail")
                        .copied()
                        .unwrap_or(current_mail_enabled);
                    current_storage_enabled = services
                        .get("storage")
                        .copied()
                        .unwrap_or(current_storage_enabled);
                }

                // [COMMENT]: Reload zone status từ DB
                if let Ok((db_status, _)) =
                    super::super::db::query_current_state(&config.database_url, &zone_id, "mail")
                        .await
                {
                    current_status = db_status;
                }
            }
            Err(err_msg) => {
                Logger::sys_error(
                    "backpressure_listener.db_error",
                    "Thất bại khi cập nhật trực tiếp status của Zone",
                    &err_msg,
                );
            }
        }
    }

    // [COMMENT]: 6. Cập nhật động status và capacity của service vào Postgres DB (Có Throttling).
    // Chỉ ghi khi: trạng thái đổi, capacity chênh >10%, hoặc quá 120 giây kể từ lần ghi cuối.
    // Lưu ý: chỉ ghi metrics cho service đang ENABLED. Service disabled không có actual_state đáng tin cậy.
    let now_instant = Instant::now();

    if current_mail_enabled {
        let should_update_mail = check_metrics_dirty(
            service_metrics_cache,
            &zone_id,
            "mail",
            &mail_status,
            mail_capacity as i32,
            now_instant,
        );
        if should_update_mail {
            if let Err(e) = super::super::db::update_zone_service_metrics(
                &config.database_url,
                &zone_id,
                "mail",
                &mail_status,
                mail_capacity as i32,
            )
            .await
            {
                Logger::sys_error(
                    "backpressure_listener.metrics_db_error",
                    "Thất bại khi cập nhật metrics Service mail vào DB",
                    &e.to_string(),
                );
            } else {
                service_metrics_cache.insert(
                    (zone_id.clone(), "mail".to_string()),
                    (mail_status.clone(), mail_capacity as i32, now_instant),
                );
            }
        }
    }

    if current_storage_enabled {
        let should_update_storage = check_metrics_dirty(
            service_metrics_cache,
            &zone_id,
            "storage",
            &storage_status,
            storage_capacity as i32,
            now_instant,
        );
        if should_update_storage {
            if let Err(e) = super::super::db::update_zone_service_metrics(
                &config.database_url,
                &zone_id,
                "storage",
                &storage_status,
                storage_capacity as i32,
            )
            .await
            {
                Logger::sys_error(
                    "backpressure_listener.metrics_db_error",
                    "Thất bại khi cập nhật metrics Service storage vào DB",
                    &e.to_string(),
                );
            } else {
                service_metrics_cache.insert(
                    (zone_id.clone(), "storage".to_string()),
                    (storage_status.clone(), storage_capacity as i32, now_instant),
                );
            }
        }
    }

    // [COMMENT]: 7. Ghi nhận heartbeat của zone vào cache RAM (đóng vai trò EnabledServicesMap).
    // current_mail_enabled và current_storage_enabled phản ánh desired_state thực tế từ DB.
    zone_heartbeats.insert(
        zone_id.clone(),
        (
            Instant::now(),
            current_status,
            current_mail_enabled,
            current_storage_enabled,
        ),
    );

    // [COMMENT]: 8. Luồng B: Xử lý workloads.hypervisors[] với race-condition guard.
    // Timestamp của bản tin stream được dùng làm sent_at để chống out-of-order heartbeats.
    let sent_at = payload.timestamp;

    for node_proto in &workloads.hypervisors {
        let node_code = &node_proto.node_code;
        if node_code.is_empty() {
            continue; // node_code bắt buộc
        }

        if let Err(e) = hypervisor_db::upsert_hypervisor_node(
            &config.database_url,
            &zone_id,
            node_code,
            &node_proto.status,
            node_proto.cpu_cores_total,
            node_proto.cpu_cores_used,
            node_proto.ram_mb_total,
            node_proto.ram_mb_used,
            node_proto.storage_gb_total,
            node_proto.storage_gb_used,
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

        // [COMMENT]: Cập nhật node heartbeat cache (Dead Man's Switch node-level 45s)
        node_heartbeats.insert((zone_id.clone(), node_code.clone()), Instant::now());
    }
}

/// [COMMENT]: Kiểm tra xem có cần ghi metrics xuống DB không (Throttle Guard).
/// Chỉ ghi khi: status đổi, capacity chênh lệch >10 đơn vị, hoặc quá 120s kể từ lần ghi cuối.
fn check_metrics_dirty(
    cache: &HashMap<(String, String), (String, i32, Instant)>,
    zone_id: &str,
    service: &str,
    new_status: &str,
    new_capacity: i32,
    now: Instant,
) -> bool {
    match cache.get(&(zone_id.to_string(), service.to_string())) {
        None => true, // Chưa có cache -> luôn ghi lần đầu
        Some((prev_status, prev_capacity, last_update)) => {
            prev_status != new_status
                || (prev_capacity - new_capacity).abs() > 10
                || now.duration_since(*last_update) > Duration::from_secs(120)
        }
    }
}
