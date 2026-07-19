use prost::Message;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

use super::reconciler;
use super::zone_proto;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// [COMMENT]: Khởi chạy task tổng hợp tài nguyên của cả cụm Dataplane và đẩy lên Platform Redis L1 (HA & Self-Healing Sync)
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

        // [COMMENT]: Duy trì kết nối multiplexed tới cả Redis L2 và Redis L1
        let mut conn_l2_opt = None;
        let mut conn_l1_opt = None;

        // [COMMENT]: Bộ đếm chu kỳ để chạy đồng bộ metadata (Reconciliation).
        // Chu kỳ Polling giãn cách lên 30 phút (720 chu kỳ * 5 giây) để tránh spam, đồng thời làm lưới an toàn (fallback) nếu rớt gói CDC.
        // Khởi tạo bằng 720 để chạy ngay lập tức khi cold start.
        let mut counter = 720;

        loop {
            // [COMMENT]: Định kỳ đồng bộ metadata của Zone (Status & Services) từ Platform L1 về L2 (30 phút/lần)
            if counter >= 720 {
                counter = 0;
                let redis_internal_zone_md = redis_internal_zone.clone();
                let redis_job_md = redis_job.clone();
                let config_md = config.clone();
                tokio::spawn(async move {
                    if let Err(e) = reconciler::sync_zone_metadata(
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

            // [COMMENT]: Đảm bảo có kết nối tới cả Redis L2 (Zone) và Redis L1 (Platform)
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
                // [COMMENT]: 1. Quét danh sách các node dataplane đang hoạt động
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

                    if let (Some(updated_at_str), Some(cpu_str), Some(ram_str), Some(workers_str)) = (
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

                // [COMMENT]: 2. Đọc trạng thái mail workload từ Redis L2 (infra:mail)
                let mail_data: std::collections::HashMap<String, String> = redis::cmd("HGETALL")
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

                // [COMMENT]: 2b. Đọc trạng thái storage workload từ Redis L2 (infra:storage)
                let storage_data: std::collections::HashMap<String, String> = redis::cmd("HGETALL")
                    .arg("infra:storage")
                    .query_async(&mut conn_l2)
                    .await
                    .unwrap_or_default();

                let storage_status = storage_data
                    .get("status")
                    .cloned()
                    .unwrap_or_else(|| "unknown".to_string());
                let storage_capacity: usize = storage_data
                    .get("capacity")
                    .and_then(|c| c.parse().ok())
                    .unwrap_or(0);

                // [COMMENT]: 2c. Đọc trạng thái hypervisor workload từ Redis L2 (infra:hypervisor)
                // HypervisorMonitor ghi vào hash này mỗi 15s, ZoneStatusGateway đọc mỗi 5s
                let hypervisor_raw: std::collections::HashMap<String, String> =
                    redis::cmd("HGETALL")
                        .arg("infra:hypervisor")
                        .query_async(&mut conn_l2)
                        .await
                        .unwrap_or_default();

                // [COMMENT]: Map JSON từ Redis L2 sang các struct Protobuf tương ứng
                #[derive(serde::Deserialize)]
                struct HypervisorCache {
                    status: String,
                    cpu_cores_total: i64,
                    cpu_cores_used: i64,
                    ram_mb_total: i64,
                    ram_mb_used: i64,
                    storage_gb_total: i64,
                    storage_gb_used: i64,
                    updated_at: i64,
                }

                let mut hypervisors = Vec::new();
                for (node_code, json_str) in &hypervisor_raw {
                    if let Ok(cache) = serde_json::from_str::<HypervisorCache>(json_str) {
                        hypervisors.push(zone_proto::HypervisorNode {
                            node_code: node_code.clone(),
                            status: cache.status,
                            cpu_cores_total: cache.cpu_cores_total,
                            cpu_cores_used: cache.cpu_cores_used,
                            ram_mb_total: cache.ram_mb_total,
                            ram_mb_used: cache.ram_mb_used,
                            storage_gb_total: cache.storage_gb_total,
                            storage_gb_used: cache.storage_gb_used,
                            updated_at: cache.updated_at,
                        });
                    }
                }

                // [COMMENT]: 3. Đóng gói payload bằng struct Protobuf (ZoneReport)
                let report = zone_proto::ZoneReport {
                    zone_id: config.zone_id.clone(),
                    timestamp: now as i64,
                    dataplane_cluster: Some(zone_proto::DataplaneCluster {
                        active_nodes: alive_nodes_count as i64,
                        avg_cpu_usage: avg_cpu,
                        avg_ram_usage: avg_ram,
                        total_active_workers: total_active_workers as i64,
                        total_max_workers: (config.max_workers * alive_nodes_count.max(1)) as i64,
                    }),
                    workloads: Some(zone_proto::Workloads {
                        mail: Some(zone_proto::MailWorkload {
                            status: mail_status,
                            capacity: mail_capacity as i32,
                        }),
                        hypervisors,
                        storage: Some(zone_proto::StorageWorkload {
                            status: storage_status,
                            capacity: storage_capacity as i32,
                        }),
                    }),
                };

                // [COMMENT]: 4. Bắn báo cáo lên Platform Redis L1
                // Serialize sang binary dùng Protobuf format để tối ưu hóa băng thông, dung lượng lưu trữ Redis.
                let mut payload_bytes = Vec::new();
                if let Err(e) = report.encode(&mut payload_bytes) {
                    Logger::sys_error(
                        "zone_gateway.serialize_error",
                        "Không thể serialize payload sang Protobuf, bỏ qua chu kỳ này",
                        &e.to_string(),
                    );
                    sleep(Duration::from_secs(5)).await;
                    counter += 1;
                    continue;
                }

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
                        .arg(&payload_bytes[..]) // binary payload
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
