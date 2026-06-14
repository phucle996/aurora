pub mod parser;
pub mod utils;
pub mod setup;

use std::collections::HashMap;
use crate::config::Config;
use crate::payload::JobPayload;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
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

        Logger::sys_info("cdc.run", "CdcStreamer: Tiến hành kết nối stream nhị phân...");

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
    // TODO: (Roadmap V2) Thay thế phần code ghi Redis Stream (XADD) bên dưới bằng Kafka Producer 
    // khi nâng cấp hạ tầng để phân phối job sang Kafka Topic (hỗ trợ thêm luồng Audit, Analytics).
    async fn process_insert(
        &self,
        fields: &HashMap<String, String>,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = fields.get("event_id").cloned().unwrap_or_default();
        let zone_id = fields.get("zone_id").cloned().unwrap_or_default();
        let job_topic = fields.get("job_topic").cloned().unwrap_or_default();
        let payload_hex = fields.get("payload").cloned().unwrap_or_default();
        let job_version_str = fields.get("job_version").cloned().unwrap_or_default();
        let resource_id = fields.get("resource_id").cloned().unwrap_or_default();
        let payload_schema_version_str = fields.get("payload_schema_version").cloned().unwrap_or_default();
        let trace_id = fields.get("trace_id").cloned().unwrap_or_default();
        let idle_str = fields.get("idle").cloned().unwrap_or_default();

        if event_id.is_empty() || zone_id.is_empty() || job_topic.is_empty() {
            Logger::sys_warn("cdc.insert", "CdcStreamer: Bỏ qua dòng insert thiếu trường quan trọng", "Missing event_id/zone_id/job_topic");
            return Ok(());
        }

        // Tăng chỉ số metrics số bản ghi WAL đã đọc từ Postgres
        crate::observability::metrics::MetricsManager::inc_wal_records_read();

        // Ghi nhận sự kiện nhận được WAL từ Postgres
        Logger::job_log(
            &event_id,
            &job_topic,
            0,
            "cdc.recv_wal",
            "CdcStreamer: Nhận được sự kiện WAL từ Postgres"
        );

        // Thiết lập scope trace_id cho toàn bộ quá trình đóng gói và gửi job lên Redis
        let trace_id_clone = trace_id.clone();
        let event_id_clone = event_id.clone();
        let job_topic_clone = job_topic.clone();
        let zone_id_clone = zone_id.clone();
        let payload_hex_clone = payload_hex.clone();
        let job_version_str_clone = job_version_str.clone();
        let resource_id_clone = resource_id.clone();
        let payload_schema_version_str_clone = payload_schema_version_str.clone();
        let idle_str_clone = idle_str.clone();

        crate::observability::otel::CURRENT_TRACE_ID.scope(trace_id_clone, async move {
            use opentelemetry::trace::{Tracer, Span, TraceContextExt};

            // 1. Phân tích ngữ cảnh cha (Parent Span) từ traceparent truyền qua WAL
            let cx = if let Some(parent_ctx) = OtelTracer::parse_traceparent(&trace_id) {
                opentelemetry::Context::current().with_remote_span_context(parent_ctx)
            } else {
                opentelemetry::Context::current()
            };

            // 2. Bắt đầu một Span nghiệp vụ mới trong Tempo
            let tracer = opentelemetry::global::tracer("job-proxy");
            let mut span = tracer.start_with_context(format!("cdc.push.{}", job_topic_clone), &cx);

            span.set_attribute(opentelemetry::KeyValue::new("job_id", event_id_clone.clone()));
            span.set_attribute(opentelemetry::KeyValue::new("zone_id", zone_id_clone.clone()));

            // Giải mã cột nhị phân (BYTEA) từ chuỗi đại diện hex truyền qua WAL
            let payload_bytes = match decode_pg_bytea(&payload_hex_clone) {
                Ok(bytes) => bytes,
                Err(e) => {
                    span.record_error(e.as_ref());
                    return Err(e);
                }
            };

            let job_version = job_version_str_clone.parse::<u32>().unwrap_or(1);
            let payload_schema_version = payload_schema_version_str_clone.parse::<u32>().unwrap_or(1);
            let idle = if idle_str_clone.is_empty() { None } else { idle_str_clone.parse::<u32>().ok() };

            // Đóng gói cấu trúc JobPayload với cột nhị phân thay cho JSON
            let payload = JobPayload {
                job_id: event_id_clone.clone(),
                job_version,
                attempt: 0,
                job_topic: job_topic_clone.clone(),
                resource_id: resource_id_clone,
                payload_schema_version,
                payload: payload_bytes,
                trace_id: trace_id.clone(),
                idle,
            };

            // Chuẩn hóa payload sang chuỗi JSON string
            let payload_str = serde_json::to_string(&payload)?;

            // Định tuyến dynamic stream key theo zone_id
            let stream_key = format!("jobs:{}", zone_id_clone);

            // Đẩy tin nhắn vào Redis Stream (Sử dụng lệnh XADD của Redis)
            let xadd_res: Result<String, redis::RedisError> = redis::cmd("XADD")
                .arg(&stream_key)
                .arg("*")
                .arg("payload")
                .arg(&payload_str)
                .query_async(redis_conn)
                .await;

            match xadd_res {
                Ok(_) => {
                    // Ghi nhận sự kiện đẩy thành công sang Redis Stream
                    Logger::job_log(
                        &event_id_clone,
                        &job_topic_clone,
                        0,
                        "cdc.push_success",
                        &format!("CdcStreamer: Đã đẩy thành công job vào Redis Stream {}", stream_key)
                    );
                    // Tăng chỉ số metrics số job đã push thành công sang Redis Stream
                    crate::observability::metrics::MetricsManager::inc_stream_jobs_pushed();
                }
                Err(e) => {
                    span.record_error(&e);
                    return Err(Box::new(e) as Box<dyn std::error::Error>);
                }
            }

            Ok(())
        }).await?;

        Ok(())
    }
}

/// Giải mã chuỗi hex biểu diễn cột BYTEA trong replication message của Postgres (dạng \x0aef...)
fn decode_pg_bytea(val: &str) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    if !val.starts_with("\\x") {
        return Ok(val.as_bytes().to_vec());
    }
    let hex_part = &val[2..];
    if hex_part.len() % 2 != 0 {
        return Err("Invalid hex length for pg bytea".into());
    }
    let mut bytes = Vec::with_capacity(hex_part.len() / 2);
    for i in (0..hex_part.len()).step_by(2) {
        let chunk = &hex_part[i..i + 2];
        let byte = u8::from_str_radix(chunk, 16)?;
        bytes.push(byte);
    }
    Ok(bytes)
}
