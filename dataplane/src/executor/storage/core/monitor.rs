use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::time::sleep;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// [COMMENT]: Bộ giám sát Storage Workload (StorageWorkloadMonitor) chạy ngầm tại Dataplane.
/// Nhiệm vụ:
///   1. Thực hiện healthcheck MinIO Cluster (TCP Port & HTTP /minio/health/live).
///   2. Cập nhật trạng thái hạ tầng vật lý vào Redis L2 internal zone (infra:storage).
///   3. Tuân thủ state machine và graceful degradation (báo cáo unknown nếu thiếu config).
pub struct StorageWorkloadMonitor;

impl StorageWorkloadMonitor {
    /// [COMMENT]: Khởi chạy vòng lặp ngầm giám sát MinIO (Self-Healing & Auto Recovery)
    pub fn start(config: Arc<Config>, redis_internal_zone: Arc<RedisClientManager>) {
        tokio::spawn(async move {
            Logger::sys_info(
                "storage_monitor.start",
                "StorageWorkloadMonitor: Khởi chạy luồng giám sát cụm MinIO L2...",
            );

            let mut conn_opt: Option<redis::aio::MultiplexedConnection> = None;
            let mut needs_recovery = true;

            loop {
                // [COMMENT]: 0. Đảm bảo kết nối Redis L2 hoạt động ổn định
                if conn_opt.is_none() {
                    match redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        Ok(conn) => {
                            conn_opt = Some(conn);
                            Logger::sys_info(
                                "storage_monitor.redis_connect",
                                "StorageWorkloadMonitor: Kết nối thành công tới Redis L2.",
                            );
                        }
                        Err(e) => {
                            Logger::sys_error(
                                "storage_monitor.redis_connection_error",
                                "Không thể kết nối tới Redis L2. Sẽ thử lại sau.",
                                &e.to_string(),
                            );
                            needs_recovery = true;
                        }
                    }
                }

                // [COMMENT]: 1. Đọc metadata của Zone từ Redis L2 để xác định mode hoạt động
                let mut zone_status = "active".to_string();
                let mut storage_enabled = "enabled".to_string();

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
                        if let Some(se) = metadata.get("service:storage") {
                            storage_enabled = se.clone();
                        }
                    }
                }

                let status;
                let capacity;

                // [COMMENT]: 2. Xử lý Logic State Machine dựa trên Metadata Zone
                if zone_status == "disabled" || storage_enabled == "disabled" {
                    // [COMMENT]: Trạng thái DISABLED: Tắt hoàn toàn, không chạy healcheck vật lý
                    status = "down";
                    capacity = 0;
                    Logger::sys_debug(
                        "storage_monitor.disabled",
                        "Storage Workload bị tắt (Zone hoặc Storage Service bị DISABLED trong Metadata).",
                    );
                } else {
                    // [COMMENT]: 3. Kiểm tra thông tin cấu hình MinIO Cluster
                    match (&config.minio_host, &config.minio_port) {
                        (Some(host), Some(port)) => {
                            // [COMMENT]: Chạy liveness check vật lý qua TCP + HTTP
                            let addr = format!("{}:{}", host, port);
                            let tcp_ok = match tokio::time::timeout(
                                Duration::from_secs(2),
                                tokio::net::TcpStream::connect(&addr),
                            )
                            .await
                            {
                                Ok(Ok(_stream)) => true,
                                _ => false,
                            };

                            if tcp_ok {
                                // [COMMENT]: TCP OK, tiến hành gửi HTTP Liveness Probe
                                match Self::fetch_liveness_raw(host, *port).await {
                                    Ok(response) => {
                                        if response.contains("200 OK") {
                                            status = "healthy";
                                            capacity = 100;
                                        } else {
                                            // [COMMENT]: TCP kết nối được nhưng HTTP trả lỗi -> Degraded
                                            status = "degraded";
                                            capacity = 50;
                                            Logger::sys_warn(
                                                "storage_monitor.http_fail",
                                                &format!("MinIO HTTP healthcheck trả về status không mong muốn: {}", response),
                                                "",
                                            );
                                        }
                                    }
                                    Err(e) => {
                                        status = "degraded";
                                        capacity = 50;
                                        Logger::sys_warn(
                                            "storage_monitor.http_error",
                                            &format!("Lỗi kết nối HTTP health check MinIO: {}", e),
                                            &e.to_string(),
                                        );
                                    }
                                }
                            } else {
                                // [COMMENT]: Cụm MinIO offline hoàn toàn ở cổng TCP
                                status = "down";
                                capacity = 0;
                                Logger::sys_error(
                                    "storage_monitor.tcp_fail",
                                    &format!("Cụm MinIO Cluster ({}) offline hoàn toàn!", addr),
                                    "TCP Handshake Failed",
                                );
                            }
                        }
                        _ => {
                            // [COMMENT]: Thiếu cấu hình -> Báo trạng thái unknown theo chỉ thị của SRE
                            status = "unknown";
                            capacity = 0;
                            Logger::sys_debug(
                                "storage_monitor.config_missing",
                                "Thiếu cấu hình MINIO_HOST/MINIO_PORT. Thiết lập trạng thái là 'unknown'.",
                            );
                        }
                    }
                }

                // [COMMENT]: 4. Ghi nhận trạng thái hạ tầng lưu trữ vật lý lên Redis L2
                if let Some(mut conn) = conn_opt.clone() {
                    let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
                        Ok(dur) => dur.as_secs(),
                        Err(_) => 0,
                    };

                    let redis_write_res = tokio::time::timeout(
                        Duration::from_secs(2),
                        redis::cmd("HSET")
                            .arg("infra:storage")
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
                                "storage_monitor.update",
                                &format!(
                                    "Đã cập nhật infra:storage (Status: {}, Capacity: {}%, Zone Status: {})",
                                    status, capacity, zone_status
                                ),
                            );
                        }
                        _ => {
                            needs_recovery = true;
                            conn_opt = None; // Reset connection để kết nối lại ở chu kỳ tiếp theo
                        }
                    }
                }

                // [COMMENT]: 5. Thời gian sleep tự điều phối (Fast Recovery khi có lỗi)
                let sleep_duration = if needs_recovery {
                    Duration::from_secs(2)
                } else {
                    Duration::from_secs(5)
                };

                sleep(sleep_duration).await;
            }
        });
    }

    /// [COMMENT]: Thực hiện HTTP GET request thô gửi tới MinIO health/live endpoint
    async fn fetch_liveness_raw(host: &str, port: u16) -> Result<String, String> {
        let addr = format!("{}:{}", host, port);
        let mut stream = tokio::time::timeout(
            Duration::from_secs(2),
            tokio::net::TcpStream::connect(&addr),
        )
        .await
        .map_err(|_| "Connect timeout".to_string())?
        .map_err(|e| e.to_string())?;

        let request = format!(
            "GET /minio/health/live HTTP/1.1\r\nHost: {}\r\nConnection: close\r\n\r\n",
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
}
