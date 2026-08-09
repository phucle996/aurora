use crate::observability::logger::Logger;
use std::time::{SystemTime, UNIX_EPOCH};

use super::policy::{ServiceSignal, ZoneDrainPolicy, ZoneSignals};
use super::proto as zone_proto;

/// [COMMENT]: Xử lý nghiệp vụ chính cho một ZoneReport nhận từ Kafka.
/// Bao gồm: decode Protobuf, đo queue, đồng bộ cache từ DB, chạy Decision Engine,
/// ghi durable Zone service observation theo timestamp fence.
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

    let cluster = payload.dataplane_cluster.unwrap_or_default();
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

    // [COMMENT]: Readiness is a fresh, timestamp-fenced Zone observation, not
    // an inference from SRE activation. Fingerprint matching in SQL prevents a
    // key_id typo or wrong Secret from authorizing undecryptable ciphertext.
    if let Err(error) = super::store::update_loaded_payload_keys(
        pg_client,
        &zone_id,
        &payload.loaded_payload_keys,
        payload.timestamp,
        payload.leader_fencing_token as i64,
    )
    .await
    {
        Logger::sys_error(
            "zone_state.payload_keys",
            "Failed to persist Zone protected-payload key readiness",
            &error.to_string(),
        );
        return Err(error.to_string());
    }

    // Physical node telemetry is exported through OTel/Grafana. Zone reports
    // may still carry the legacy field during rollout, but JO deliberately
    // does not persist it as business state.
    Ok(())
}
