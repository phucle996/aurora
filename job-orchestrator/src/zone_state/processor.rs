use crate::observability::logger::Logger;
use std::time::{SystemTime, UNIX_EPOCH};

use super::nodes as hypervisor_db;
use super::policy::{ServiceSignal, ZoneDrainPolicy, ZoneSignals};
use super::proto as zone_proto;

/// [COMMENT]: Xử lý nghiệp vụ chính cho một ZoneReport nhận từ Kafka.
/// Bao gồm: decode Protobuf, đo queue, đồng bộ cache từ DB, chạy Decision Engine,
/// ghi durable observation theo timestamp fence và upsert hypervisor nodes.
///
/// NGUYÊN TẮC ENABLED-ONLY: DecisionEngine chỉ nhận enabled_services từ zone_heartbeats cache.
/// Service disabled không tham gia vào bất kỳ quyết định trạng thái nào.
///
/// Chỉ trả Ok khi toàn bộ side effect cần thiết hoàn tất để listener được commit Kafka offset.
pub async fn process_report(
    pg_client: &tokio_postgres::Client,
    zone_id: String,
    payload: zone_proto::ZoneReport,
) -> Result<(), String> {
    // [COMMENT]: Stream envelope và payload phải cùng Zone; timestamp bounded mới được làm DB fence.
    let now_unix_seconds = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs().min(i64::MAX as u64) as i64)
        .unwrap_or_default();
    if payload.zone_id != zone_id
        || payload.timestamp <= 0
        || payload.timestamp > now_unix_seconds.saturating_add(300)
        || payload.timestamp < now_unix_seconds.saturating_sub(86_400)
    {
        Logger::sys_warn(
            "backpressure_listener.report_scope",
            "Zone report envelope or observation timestamp is invalid",
            "ZONE_REPORT_SCOPE_OR_TIME_INVALID",
        );
        return Err("zone report scope or timestamp is invalid".to_string());
    }

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

    // [COMMENT]: Dataplane đo Kafka lag bằng chính group credential; JO không cross-query broker theo Zone.
    let queue_len = cluster.job_queue_lag.max(0);
    let pending_len = 0;

    let policy_state = super::store::query_zone_policy_state(pg_client, &zone_id)
        .await
        .map_err(|error| format!("load Zone policy state failed: {error}"))?;
    let current_status = policy_state.status;
    let mut current_mail_enabled = policy_state.mail_enabled;
    let mut current_storage_enabled = policy_state.storage_enabled;

    // [COMMENT]: 4. Chạy Decision Engine — enabled-only evaluation.
    // Chỉ pass vào enabled services. DecisionEngine chỉ trả về target_zone_status.
    // Decision Engine KHÔNG tự toggle desired_state của service — đó là quyền của SRE.
    let target_status = if cluster.job_queue_lag_stale {
        // [COMMENT]: Stale lag không được tự động chuyển state; giữ DB state đến report tin cậy kế tiếp.
        current_status.clone()
    } else {
        ZoneDrainPolicy::evaluate(ZoneSignals {
            queue_lag: queue_len,
            pending_jobs: pending_len,
            cpu_ratio: avg_cpu,
            ram_ratio: avg_ram,
            mail: ServiceSignal {
                enabled: current_mail_enabled,
                status: &mail_status,
                capacity_percent: mail_capacity,
            },
            storage: ServiceSignal {
                enabled: current_storage_enabled,
                status: &storage_status,
                capacity_percent: storage_capacity,
            },
            current_status: &current_status,
        })
    };

    // [COMMENT]: 5. Thực hiện cập nhật trực tiếp Postgres DB nếu zone_status thay đổi.
    // Chuyển Error sang String để vượt ranh giới async Send trait của Rust.
    if target_status != current_status {
        let update_result = super::store::update_zone_status(pg_client, &zone_id, &target_status)
            .await
            .map_err(|e| e.to_string());

        match update_result {
            Ok(true) => {
                // The next report reloads lifecycle from PostgreSQL; no
                // process-local SRE state survives a Kafka rebalance.
            }
            Ok(false) => {
                // [COMMENT]: DB Guard từ chối do vi phạm chuyển dịch trạng thái.
                // Lập tức query lại DB để sửa sai cache RAM (Self-Correcting Cache).
                if let Ok(corrected) =
                    super::store::query_zone_policy_state(pg_client, &zone_id).await
                {
                    current_mail_enabled = corrected.mail_enabled;
                    current_storage_enabled = corrected.storage_enabled;
                }
            }
            Err(err_msg) => {
                Logger::sys_error(
                    "backpressure_listener.db_error",
                    "Thất bại khi cập nhật trực tiếp status của Zone",
                    &err_msg,
                );
                return Err(err_msg);
            }
        }
    }

    // One timestamp-fenced batch halves DB round-trips. Throttling observations
    // would create false-down transitions after JO failover.
    if let Err(error) = super::store::update_reported_service_health(
        pg_client,
        &zone_id,
        &mail_status,
        current_mail_enabled,
        &storage_status,
        current_storage_enabled,
        payload.timestamp,
    )
    .await
    {
        Logger::sys_error(
            "zone_state.health",
            "Failed to update reported Zone service health",
            &error.to_string(),
        );
        return Err(error.to_string());
    }

    // Hypervisor observations use the same report timestamp race fence.
    // Timestamp của bản tin stream được dùng làm sent_at để chống out-of-order heartbeats.
    let sent_at = payload.timestamp;

    for node_proto in &workloads.hypervisors {
        let node_code = &node_proto.node_code;
        if node_code.is_empty() {
            continue; // node_code bắt buộc
        }

        let observation = hypervisor_db::HypervisorObservation {
            node_code,
            status: &node_proto.status,
            cpu_cores_total: node_proto.cpu_cores_total,
            cpu_cores_used: node_proto.cpu_cores_used,
            ram_mb_total: node_proto.ram_mb_total,
            ram_mb_used: node_proto.ram_mb_used,
            storage_gb_total: node_proto.storage_gb_total,
            storage_gb_used: node_proto.storage_gb_used,
            observed_at: sent_at,
        };
        if let Err(e) =
            hypervisor_db::upsert_hypervisor_node(pg_client, &zone_id, &observation).await
        {
            Logger::sys_error(
                "backpressure_listener.hypervisor_upsert",
                &format!(
                    "Lỗi upsert hypervisor node '{}' của Zone {}",
                    node_code, zone_id
                ),
                &e.to_string(),
            );
            return Err(e.to_string());
        }
    }
    Ok(())
}
