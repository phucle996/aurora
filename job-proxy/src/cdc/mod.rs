pub mod parser;
pub mod utils;

use std::collections::HashMap;
use crate::config::Config;
use crate::payload::JobPayload;
use tokio_postgres::NoTls;
use crate::logger::Logger;
use pgwire_replication::{ReplicationClient, ReplicationConfig, ReplicationEvent, Lsn};

use parser::{PgOutputRelation, parse_relation_message, parse_insert_message, read_u32};
use utils::parse_pg_config;

/// CdcStreamer chịu trách nhiệm kết nối và duy trì luồng stream logical replication từ PostgreSQL.
pub struct CdcStreamer {
    config: Config,
    redis_client: redis::Client,
}

impl CdcStreamer {
    /// Khởi tạo một CdcStreamer mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self {
            config,
            redis_client,
        }
    }

    /// Khởi chạy luồng stream nhận và phân phối sự kiện từ WAL theo giao thức push-based.
    /// P1: Tích hợp Reconnect Loop ở ngoài để đảm bảo ứng dụng không bị crash khi kết nối mạng chập chờn.
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info("cdc.run", "CdcStreamer: Khởi chạy luồng giám sát CDC Outbox với cơ chế tự động reconnect...");
        
        loop {
            // Thực thi kết nối và bắt đầu nhận stream
            if let Err(e) = self.run_replication_stream().await {
                Logger::sys_error(
                    "cdc.run", 
                    "CdcStreamer: Gặp lỗi trong luồng replication stream. Tiến hành reconnect sau 5 giây...", 
                    &e.to_string()
                );
                // Đợi 5 giây trước khi thực hiện kết nối lại để tránh spam kết nối liên tục khi DB sập hẳn
                tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            } else {
                Logger::sys_info("cdc.run", "CdcStreamer: Luồng replication kết thúc bình thường. Tiến hành reconnect...");
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
            }
        }
    }

    /// Kết nối và chạy stream logical replication cho một phiên kết nối cụ thể.
    /// Nếu phiên này đứt gãy hoặc gặp lỗi ghi Redis, hàm sẽ trả về Err để vòng lặp run() chính thực hiện reconnect.
    async fn run_replication_stream(&self) -> Result<(), Box<dyn std::error::Error>> {
        // P2: Phân tích thông số kết nối từ DATABASE_URL sử dụng thư viện tokio-postgres chuẩn để tránh lỗi parse thủ công
        let (pg_host, pg_port, pg_user, pg_password, pg_db) = parse_pg_config(&self.config.database_url)
            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;

        // 1. Kết nối PostgreSQL thông thường để kiểm tra và tự tạo publication/slot
        let (client, connection) = tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error("cdc.postgres", "CdcStreamer: Lỗi kết nối PostgreSQL kiểm tra hạ tầng", &e.to_string());
            }
        });

        // Set search path đến mail schema
        client.execute("SET search_path TO mail, public", &[]).await?;

        // Kiểm tra xem Publication đã tồn tại chưa
        let pub_check = client.query(
            "SELECT 1 FROM pg_publication WHERE pubname = $1",
            &[&self.config.publication_name],
        ).await?;

        if pub_check.is_empty() {
            Logger::sys_info("cdc.run", &format!("CdcStreamer: Tạo publication '{}' cho bảng mail_outbox_records...", self.config.publication_name));
            let create_pub_sql = format!(
                "CREATE PUBLICATION {} FOR TABLE mail_outbox_records",
                self.config.publication_name
            );
            if let Err(err) = client.execute(&create_pub_sql, &[]).await {
                let err_str = err.to_string();
                // Bắt lỗi trùng lặp đối tượng (duplicate_object / 42710) phòng trường hợp tranh chấp HA khi nhiều instance chạy song song
                if err_str.contains("already exists") || err_str.contains("42710") {
                    Logger::sys_warn("cdc.run", "CdcStreamer: Publication đã tồn tại (bỏ qua do tranh chấp HA)", &err_str);
                } else {
                    return Err(err.into());
                }
            }
        }

        // Kiểm tra xem Replication Slot đã tồn tại chưa
        let slot_check = client.query(
            "SELECT 1 FROM pg_replication_slots WHERE slot_name = $1 AND plugin = 'pgoutput'",
            &[&self.config.slot_name],
        ).await?;

        if slot_check.is_empty() {
            Logger::sys_info("cdc.run", &format!("CdcStreamer: Tạo logical replication slot '{}' với plugin pgoutput...", self.config.slot_name));
            let create_slot_sql = format!(
                "SELECT lsn FROM pg_create_logical_replication_slot('{}', 'pgoutput')",
                self.config.slot_name
            );
            if let Err(err) = client.query(&create_slot_sql, &[]).await {
                let err_str = err.to_string();
                // Bắt lỗi trùng lặp đối tượng (duplicate_object / 42710) phòng trường hợp tranh chấp HA khi nhiều instance chạy song song
                if err_str.contains("already exists") || err_str.contains("42710") {
                    Logger::sys_warn("cdc.run", "CdcStreamer: Replication slot đã tồn tại (bỏ qua do tranh chấp HA)", &err_str);
                } else {
                    return Err(err.into());
                }
            }
        }

        // Đóng kết nối kiểm tra hạ tầng SQL thường
        drop(client);
        Logger::sys_info("cdc.run", "CdcStreamer: Hạ tầng replication đã sẵn sàng. Tiến hành kết nối stream nhị phân...");

        // 2. Khởi tạo cấu hình cho pgwire-replication client
        let config = ReplicationConfig {
            host: pg_host,
            port: pg_port,
            user: pg_user,
            password: pg_password,
            database: pg_db,
            slot: self.config.slot_name.clone(),
            publication: self.config.publication_name.clone(),
            start_lsn: Lsn::ZERO,
            ..Default::default()
        };

        // Thực hiện kết nối stream logical replication
        let mut client = ReplicationClient::connect(config).await?;

        // Khởi tạo kết nối Redis Multiplexed Client
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        // Bảng cache lưu schema relation (relation_id -> PgOutputRelation)
        let mut relation_map: HashMap<u32, PgOutputRelation> = HashMap::new();

        Logger::sys_info("cdc.run", &format!("CdcStreamer: Đang lắng nghe thay đổi từ logical slot: {}...", self.config.slot_name));

        // 3. Vòng lặp nhận sự kiện trực tiếp từ WAL (Push-based streaming)
        while let Some(event) = client.recv().await? {
            match event {
                // XLogData chứa các message pgoutput thô
                ReplicationEvent::XLogData { wal_end, data, .. } => {
                    if data.is_empty() {
                        client.update_applied_lsn(wal_end);
                        continue;
                    }
                    
                    let tag = data[0];
                    match tag {
                        // Nhận định nghĩa Relation ('R') để cập nhật schema map
                        b'R' => {
                            match parse_relation_message(&data) {
                                Ok(rel) => {
                                    Logger::sys_info(
                                        "cdc.relation",
                                        &format!("Schema table {}.{} (ID: {}) được cập nhật: {} columns", 
                                            rel.schema_name, rel.relation_name, rel.relation_id, rel.columns.len()
                                        )
                                    );
                                    relation_map.insert(rel.relation_id, rel);
                                }
                                Err(err) => {
                                    Logger::sys_error("cdc.relation", "Lỗi phân tích Relation message", &err);
                                }
                            }
                        }
                        // Nhận sự kiện Insert ('I') trên một relation
                        b'I' => {
                            let mut offset = 1;
                            if let Ok(relation_id) = read_u32(&data, &mut offset) {
                                if let Some(rel) = relation_map.get(&relation_id) {
                                    // Chỉ xử lý bản ghi thuộc bảng mail_outbox_records
                                    if rel.relation_name == "mail_outbox_records" {
                                        let fields = parse_insert_message(&data, &rel.columns)
                                            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
                                        
                                        // P0: NẾU GHI REDIS THẤT BẠI -> Ném lỗi thoát vòng lặp kết nối ngay lập tức!
                                        // Điều này ngăn cản việc cập nhật applied LSN lên Postgres bên dưới.
                                        // Postgres giữ WAL và gửi lại (replay) từ vị trí LSN chưa ACK cuối cùng khi kết nối lại.
                                        self.process_insert(&fields, &mut redis_conn).await?;
                                    }
                                }
                            }
                        }
                        _ => {}
                    }
                    
                    // Cập nhật LSN đã xử lý thành công để phản hồi StandbyStatusUpdate về Postgres giải phóng WAL.
                    // Chỉ chạy khi các hàm xử lý ở trên (như process_insert) hoàn thành thành công.
                    client.update_applied_lsn(wal_end);
                }
                // Server gửi nhịp tim KeepAlive duy trì kết nối
                ReplicationEvent::KeepAlive { wal_end, reply_requested, .. } => {
                    if reply_requested {
                        // Xác nhận vị trí LSN hiện tại để ngăn slot bị nghẽn
                        client.update_applied_lsn(wal_end);
                    }
                }
                ReplicationEvent::StoppedAt { reached } => {
                    Logger::sys_warn("cdc.run", "CdcStreamer: Dừng stream WAL tại LSN", &reached.to_string());
                    break;
                }
                _ => {}
            }
        }

        Ok(())
    }

    /// Xử lý sự kiện INSERT đã giải mã, định tuyến và push sang Redis Stream jobs:<zone_id>
    async fn process_insert(
        &self,
        fields: &HashMap<String, String>,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = fields.get("event_id").cloned().unwrap_or_default();
        let zone_id = fields.get("zone_id").cloned().unwrap_or_default();
        let job_topic = fields.get("job_topic").cloned().unwrap_or_default();
        let payload_json = fields.get("payload_json").cloned().unwrap_or_default();
        let job_version_str = fields.get("job_version").cloned().unwrap_or_default();
        let resource_id = fields.get("resource_id").cloned().unwrap_or_default();
        let payload_schema_version_str = fields.get("payload_schema_version").cloned().unwrap_or_default();
        let trace_id = fields.get("trace_id").cloned().unwrap_or_default();
        let idle_str = fields.get("idle").cloned().unwrap_or_default();

        if event_id.is_empty() || zone_id.is_empty() || job_topic.is_empty() {
            Logger::sys_warn("cdc.insert", "CdcStreamer: Bỏ qua dòng insert thiếu trường quan trọng", "Missing event_id/zone_id/job_topic");
            return Ok(());
        }

        // Tích hợp OpenTelemetry tracing: inject trace context từ WAL vào Span nghiệp vụ
        if !trace_id.is_empty() {
            crate::otel::OtelTracer::inject_trace_context(&trace_id);
        }

        let job_version = job_version_str.parse::<u32>().unwrap_or(1);
        let payload_schema_version = payload_schema_version_str.parse::<u32>().unwrap_or(1);
        
        // idle = None nếu giá trị trong db là NULL (không giới hạn thời gian lease)
        let idle = if idle_str.is_empty() {
            None
        } else {
            idle_str.parse::<u32>().ok()
        };

        // Đóng gói cấu trúc JobPayload
        let payload = JobPayload {
            job_id: event_id.clone(),
            job_version,
            attempt: 0, // Luôn mặc định là 0 cho lần chạy đầu tiên (đã bỏ attempts trong db)
            job_topic: job_topic.clone(),
            resource_id,
            payload_schema_version,
            payload_json,
            trace_id,
            idle,
        };

        // Chuẩn hóa payload sang chuỗi JSON string
        let payload_str = serde_json::to_string(&payload)?;

        // Định tuyến dynamic stream key theo zone_id
        let stream_key = format!("jobs:{}", zone_id);

        Logger::job_log(&event_id, &job_topic, 0, "cdc.push", &format!("Push job sang Redis Stream {}", stream_key));

        // Đẩy tin nhắn vào Redis Stream (Sử dụng lệnh XADD của Redis)
        let _: String = redis::cmd("XADD")
            .arg(&stream_key)
            .arg("*")
            .arg("payload")
            .arg(&payload_str)
            .query_async(redis_conn)
            .await?;

        Ok(())
    }
}
