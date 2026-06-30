use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::time::sleep;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// Bộ giám sát Mail Workload (MailWorkloadMonitor) chạy ngầm tại Dataplane.
/// Nhiệm vụ:
///   1. Thực hiện healthcheck Stalwart Mail Server (LMTP port & HTTP Metrics).
///   2. Tính toán capacity thực tế.
///   3. Cập nhật trạng thái hạ tầng vật lý vào Redis L2 internal zone (infra:mail).
///   4. Tự điều phối trạng thái dựa trên zone metadata (State Machine).
pub struct MailWorkloadMonitor;

impl MailWorkloadMonitor {
    /// Khởi chạy vòng lặp ngầm giám sát Mail workload (HA & Self-Healing & State Machine)
    pub fn start(config: Arc<Config>, redis_internal_zone: Arc<RedisClientManager>) {
        tokio::spawn(async move {
            Logger::sys_info(
                "mail_monitor.start",
                "MailWorkloadMonitor: Khởi chạy luồng giám sát Stalwart Mail L2...",
            );

            // Cấu hình Stalwart HTTP Port từ biến môi trường
            let stalwart_http_port: u16 = std::env::var("STALWART_HTTP_PORT")
                .ok()
                .and_then(|p| p.parse().ok())
                .unwrap_or(8080);

            // Duy trì kết nối Multiplexed tới Redis L2
            let mut conn_opt: Option<redis::aio::MultiplexedConnection> = None;
            let mut needs_recovery = true;

            loop {
                // 0. Đảm bảo kết nối Redis L2 hoạt động
                if conn_opt.is_none() {
                    match redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        Ok(conn) => {
                            conn_opt = Some(conn);
                            Logger::sys_info(
                                "mail_monitor.redis_connect",
                                "MailWorkloadMonitor: Kết nối thành công tới Redis L2.",
                            );
                        }
                        Err(e) => {
                            Logger::sys_error(
                                "mail_monitor.redis_connection_error",
                                "Không thể kết nối tới Redis L2. Sẽ thử lại sau.",
                                &e.to_string(),
                            );
                            needs_recovery = true;
                        }
                    }
                }

                // 1. Đọc metadata của Zone từ Redis L2
                let mut zone_status = "active".to_string();
                let mut mail_enabled = "enabled".to_string();

                if let Some(mut conn) = conn_opt.clone() {
                    let metadata_res: Result<
                        std::collections::HashMap<String, String>,
                        redis::RedisError,
                    > = redis::cmd("HGETALL")
                        .arg("infra:zone:metadata")
                        .query_async(&mut conn)
                        .await;

                    if let Ok(metadata) = metadata_res {
                        if let Some(s) = metadata.get("status") {
                            zone_status = s.clone();
                        }
                        if let Some(me) = metadata.get("service:mail") {
                            mail_enabled = me.clone();
                        }
                    }
                }

                let host = &config.stalwart_lmtp_host;
                let lmtp_port = config.stalwart_lmtp_port;

                let mut status = "down";
                let mut capacity = 0;
                let mut queue_size = 0.0;

                // 2. Chạy logic State Machine dựa trên Metadata Zone
                if zone_status == "disabled" || mail_enabled == "disabled" {
                    // Trạng thái DISABLED: Tắt hoàn toàn, không chạy healcheck
                    status = "down";
                    capacity = 0;
                    Logger::sys_debug(
                        "mail_monitor.disabled",
                        "Mail Workload bị tắt (Zone hoặc Mail Service bị DISABLED trong Metadata).",
                    );
                } else {
                    // Kiểm tra TCP handshake cơ bản tới LMTP port (LMTP Healthcheck)
                    let lmtp_addr = format!("{}:{}", host, lmtp_port);
                    let tcp_ok = match tokio::time::timeout(
                        Duration::from_secs(2),
                        tokio::net::TcpStream::connect(&lmtp_addr),
                    )
                    .await
                    {
                        Ok(Ok(_stream)) => true,
                        _ => false,
                    };

                    if tcp_ok {
                        status = "healthy";
                        capacity = 100;

                        if zone_status == "planned" {
                            // Trạng thái PLANNED: Chỉ cần healcheck LMTP cơ bản để báo cáo healthy cho SRE, không đọc SMTP queue nặng
                            Logger::sys_debug(
                                "mail_monitor.planned",
                                "Zone đang ở trạng thái PLANNED. Chỉ chạy healcheck cơ bản, bỏ qua quét hàng đợi.",
                            );
                        } else {
                            // Các trạng thái ACTIVE, MAINTENANCE, DRAINING: Chạy đầy đủ để đo đạc capacity
                            match Self::fetch_metrics_raw(host, stalwart_http_port).await {
                                Ok(metrics_body) => {
                                    if let Some(q_size) = Self::parse_metric(
                                        &metrics_body,
                                        "stalwart_smtp_queue_size",
                                    ) {
                                        queue_size = q_size;
                                        let max_allowed_queue = 5000.0;
                                        let queue_pct = (queue_size / max_allowed_queue).min(1.0);
                                        capacity = ((1.0 - queue_pct) * 100.0) as usize;

                                        if capacity < 10 {
                                            status = "degraded"; // Queue nghẽn nặng
                                        }
                                    }
                                }
                                Err(e) => {
                                    status = "degraded";
                                    capacity = 50;
                                    Logger::sys_debug(
                                        "mail_monitor.fetch_metrics_failed",
                                        &format!("Không thể đọc HTTP metrics từ Stalwart. Chuyển sang degraded. Lỗi: {}", e),
                                    );
                                }
                            }
                        }
                    } else {
                        Logger::sys_error(
                            "mail_monitor.lmtp_check",
                            &format!("Stalwart LMTP Server ({}) offline hoàn toàn!", lmtp_addr),
                            "TCP Handshake Failed",
                        );
                    }
                }

                // 3. Ghi nhận trạng thái hạ tầng mail vật lý lên Redis L2
                if let Some(mut conn) = conn_opt.clone() {
                    let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
                        Ok(dur) => dur.as_secs(),
                        Err(_) => 0,
                    };

                    let redis_write_res = tokio::time::timeout(
                        Duration::from_secs(2),
                        redis::cmd("HSET")
                            .arg("infra:mail")
                            .arg("status")
                            .arg(status)
                            .arg("capacity")
                            .arg(capacity)
                            .arg("updated_at")
                            .arg(now)
                            .query_async::<_, ()>(&mut conn),
                    )
                    .await;

                    match redis_write_res {
                        Ok(Ok(())) => {
                            needs_recovery = false;
                            Logger::sys_debug(
                                "mail_monitor.update",
                                &format!(
                                    "Đã cập nhật infra:mail (Status: {}, Capacity: {}%, Queue: {}, Zone Status: {})",
                                    status, capacity, queue_size, zone_status
                                ),
                            );
                        }
                        _ => {
                            needs_recovery = true;
                            conn_opt = None; // Reset connection khi ghi lỗi/timeout để tái khởi tạo
                        }
                    }
                }

                // 4. Cơ chế sleep tự thích ứng (Fast Recovery)
                let sleep_duration = if needs_recovery {
                    Duration::from_secs(2)
                } else {
                    Duration::from_secs(5)
                };

                sleep(sleep_duration).await;
            }
        });
    }

    /// Đọc plaintext HTTP response thô từ Stalwart metrics endpoint (TCP socket client thô)
    async fn fetch_metrics_raw(host: &str, port: u16) -> Result<String, String> {
        let addr = format!("{}:{}", host, port);
        let mut stream = tokio::time::timeout(
            Duration::from_secs(2),
            tokio::net::TcpStream::connect(&addr),
        )
        .await
        .map_err(|_| "Connect timeout".to_string())?
        .map_err(|e| e.to_string())?;

        let request = format!(
            "GET /metrics HTTP/1.1\r\nHost: {}\r\nConnection: close\r\n\r\n",
            host
        );
        stream
            .write_all(request.as_bytes())
            .await
            .map_err(|e| e.to_string())?;

        let mut response = String::new();
        tokio::time::timeout(Duration::from_secs(2), stream.read_to_string(&mut response))
            .await
            .map_err(|_| "Read timeout".to_string())?
            .map_err(|e| e.to_string())?;

        Ok(response)
    }

    /// Hàm phụ parse chỉ số metric từ plaintext body của Prometheus exporter
    fn parse_metric(body: &str, metric_name: &str) -> Option<f64> {
        for line in body.lines() {
            if !line.starts_with('#') && line.starts_with(metric_name) {
                let parts: Vec<&str> = line.split_whitespace().collect();
                if parts.len() >= 2 {
                    if let Ok(val) = parts[1].parse::<f64>() {
                        return Some(val);
                    }
                }
            }
        }
        None
    }
}
