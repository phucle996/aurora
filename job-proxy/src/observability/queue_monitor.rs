use crate::config::Config;
use crate::observability::logger::Logger;
use crate::observability::metrics::MetricsManager;
use crate::transport::grpc::core_rpc::backpressure_service_client::BackpressureServiceClient;
use crate::transport::grpc::send_backpressure_report;
use tokio_postgres::NoTls;
use std::time::Duration;
use std::collections::HashMap;
use tokio::time::Instant;
use tonic::transport::Channel;

/// Cấu trúc giám sát tải hàng đợi và tin nhắn chờ (Queue Monitor)
pub struct QueueMonitor {
    config: Config,
    redis_client: redis::Client,
}

impl QueueMonitor {
    /// Khởi tạo một thực thể QueueMonitor mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self { config, redis_client }
    }

    /// Khởi chạy vòng lặp giám sát chạy ngầm (Background Loop)
    pub async fn run(&self) {
        Logger::sys_info(
            "queue_monitor.run",
            "QueueMonitor: Bắt đầu chạy ngầm giám sát độ trễ hàng đợi...",
        );

        let mut client: Option<BackpressureServiceClient<Channel>> = None;
        let mut last_congested_states: HashMap<String, bool> = HashMap::new();
        let mut last_sent_timestamps: HashMap<String, Instant> = HashMap::new();

        // Vòng lặp vô hạn chạy định kỳ thu thập chỉ số
        loop {
            // Thực hiện chu kỳ kiểm tra tải của từng zone
            if let Err(e) = self.monitor_cycle(&mut client, &mut last_congested_states, &mut last_sent_timestamps).await {
                Logger::sys_warn(
                    "queue_monitor.run",
                    "QueueMonitor: Lỗi trong chu kỳ giám sát hàng đợi, sẽ thử lại sau 5s...",
                    &e.to_string(),
                );
            }
            
            // Đợi 5 giây trước khi thực hiện chu kỳ quét tiếp theo (Không block luồng chính)
            tokio::time::sleep(Duration::from_secs(5)).await;
        }
    }

    /// Thực hiện một chu kỳ quét: Đọc DB để lấy active zones -> Quét Redis Stream Length & Pending
    async fn monitor_cycle(
        &self,
        client: &mut Option<BackpressureServiceClient<Channel>>,
        last_congested_states: &mut HashMap<String, bool>,
        last_sent_timestamps: &mut HashMap<String, Instant>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        // 1. Thiết lập kết nối tạm thời tới Postgres để đọc danh sách zone
        // Sử dụng kết nối không mã hóa TLS (NoTls)
        let (pg_client, connection) = tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        
        // Spawn luồng chạy ngầm của tokio-postgres để xử lý I/O truyền nhận gói tin
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error(
                    "queue_monitor.postgres",
                    "QueueMonitor: Lỗi luồng kết nối chạy ngầm của PostgreSQL",
                    &e.to_string(),
                );
            }
        });

        // 2. Lấy danh sách các zone đang hoạt động (status = 'active') từ schema core
        let rows = pg_client
            .query("SELECT id::text FROM core.zones WHERE status = 'active'", &[])
            .await?;

        let mut active_zones = Vec::new();
        for row in rows {
            let zone_id: String = row.get(0);
            active_zones.push(zone_id);
        }

        // Nếu không có zone nào đang active, kết thúc chu kỳ sớm để tiết kiệm tài nguyên
        if active_zones.is_empty() {
            return Ok(());
        }

        // 3. Khởi tạo kết nối Multiplexed tới Redis (Hỗ trợ truy cập đồng thời, hiệu năng cao)
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        // 4. Lặp qua từng zone để đo lường các chỉ số hàng đợi và báo cáo nghẽn
        for zone_id in active_zones {
            let stream_key = format!("jobs:{}", zone_id);

            // Truy vấn độ dài hàng đợi hiện tại trong Redis Stream (Lệnh XLEN tốn chi phí O(1))
            let len: i64 = redis::cmd("XLEN")
                .arg(&stream_key)
                .query_async(&mut redis_conn)
                .await
                .unwrap_or(0);

            // Truy vấn số tin nhắn đã tiêu thụ nhưng chưa ACK (XPENDING) của consumer group 'dataplane_group'
            let pending_res: Result<Vec<redis::Value>, redis::RedisError> = redis::cmd("XPENDING")
                .arg(&stream_key)
                .arg("dataplane_group")
                .query_async(&mut redis_conn)
                .await;

            let pending: i64 = match pending_res {
                Ok(values) => {
                    if !values.is_empty() {
                        // Phần tử đầu tiên trong mảng trả về là tổng số pending messages
                        match &values[0] {
                            redis::Value::Int(n) => *n,
                            _ => 0,
                        }
                    } else {
                        0
                    }
                }
                // Trả về 0 nếu Consumer Group chưa được khởi tạo ở chu kỳ chạy đầu tiên
                Err(_) => 0,
            };

            // 5. Đẩy số liệu thu thập được lên Prometheus Registry
            MetricsManager::set_queue_len(&zone_id, len);
            MetricsManager::set_pending_len(&zone_id, pending);

            // 6. Cơ chế quyết định Trạng thái Quá tải (Backpressure Decision Engine)
            // Ngưỡng nghẽn: hàng đợi chính > 5000 job HOẶC hàng đợi chưa ACK > 500 job
            let congested = len > 5000 || pending > 500;
            let last_congested = *last_congested_states.get(&zone_id).unwrap_or(&false);
            let last_sent = last_sent_timestamps.get(&zone_id);

            // Gửi gRPC nếu:
            // a) Trạng thái nghẽn thay đổi (Báo cáo lập tức)
            // b) Vẫn nghẽn và đã trôi qua 15s kể từ lần báo cáo trước (Gia hạn lease trên Controlplane)
            let should_send = if congested != last_congested {
                true
            } else if congested && last_sent.map_or(true, |t| t.elapsed() >= Duration::from_secs(15)) {
                true
            } else {
                false
            };

            if should_send {
                Logger::sys_info(
                    "queue_monitor.backpressure",
                    &format!(
                        "Zone {} thay đổi trạng thái quá tải: congested={}, len={}, pending={}. Đang truyền tải gRPC...",
                        zone_id, congested, len, pending
                    ),
                );

                let epoch = std::time::SystemTime::now()
                    .duration_since(std::time::SystemTime::UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_nanos() as i64;

                let congestion_rate = if congested {
                    (len as f64 / 5000.0).max(pending as f64 / 500.0).min(1.0)
                } else {
                    0.0
                };

                if let Err(e) = send_backpressure_report(
                    client,
                    &self.config,
                    &zone_id,
                    len,
                    pending,
                    congested,
                    epoch,
                    congestion_rate,
                ).await {
                    Logger::sys_error(
                        "queue_monitor.grpc_error",
                        &format!("Thất bại khi bắn gRPC backpressure cho zone {}", zone_id),
                        &e.to_string(),
                    );
                    // Reset client để buộc khởi tạo lại kết nối ở chu kỳ tiếp theo
                    *client = None;
                } else {
                    last_congested_states.insert(zone_id.clone(), congested);
                    last_sent_timestamps.insert(zone_id.clone(), Instant::now());
                }
            }
        }

        Ok(())
    }
}
