pub mod parser;
pub mod utils;
pub mod setup;
use std::collections::HashMap;
use crate::config::Config;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use pgwire_replication::{ReplicationClient, ReplicationConfig, ReplicationEvent, Lsn};

use parser::{PgOutputRelation, parse_relation_message, parse_insert_message, read_u32};
use utils::parse_pg_config;

/// CdcStreamer chịu trách nhiệm kết nối và duy trì luồng stream logical replication từ PostgreSQL.
pub struct CdcStreamer {
    config: Config,
    redis_client: redis::Client,
    active_zones_cache: tokio::sync::Mutex<Option<(Vec<String>, std::time::Instant)>>,
}

impl CdcStreamer {
    /// Khởi tạo một CdcStreamer mới
    pub fn new(config: Config, redis_client: redis::Client) -> Self {
        Self {
            config,
            redis_client,
            active_zones_cache: tokio::sync::Mutex::new(None),
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
                                    // Kiểm tra xem tên bảng này có nằm trong danh sách cdc_sources hay không
                                    let is_monitored = self.config.cdc_sources.iter().any(|source| {
                                        let parts: Vec<&str> = source.split('.').collect();
                                        let table_name = if parts.len() == 2 { parts[1] } else { parts[0] };
                                        rel.relation_name == table_name
                                    });

                                    if is_monitored {
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
        // Trích xuất routing_scope thay vì zone_id
        let routing_scope = fields.get("routing_scope").cloned().unwrap_or_default();
        let job_topic = fields.get("job_topic").cloned().unwrap_or_default();
        let payload_hex = fields.get("payload").cloned().unwrap_or_default();
        let job_version_str = fields.get("job_version").cloned().unwrap_or_default();
        let resource_id = fields.get("resource_id").cloned().unwrap_or_default();
        let payload_schema_version_str = fields.get("payload_schema_version").cloned().unwrap_or_default();
        let trace_id_raw = fields.get("trace_id").cloned().unwrap_or_default();
        // Giải mã trace_id cột nhị phân (BYTEA) từ chuỗi đại diện hex truyền qua WAL
        let trace_id_bytes = decode_pg_bytea(&trace_id_raw).unwrap_or_default();
        // Chuyển đổi sang chuỗi hex 32 ký tự để tương thích với OTel spans và logs cục bộ
        let trace_id_hex = trace_id_bytes.iter().map(|b| format!("{:02x}", b)).collect::<String>();
        let idle_str = fields.get("idle").cloned().unwrap_or_default();

        if event_id.is_empty() || routing_scope.is_empty() || job_topic.is_empty() {
            Logger::sys_warn("cdc.insert", "CdcStreamer: Bỏ qua dòng insert thiếu trường quan trọng", "Missing event_id/routing_scope/job_topic");
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
        let trace_id_hex_clone = trace_id_hex.clone();
        let event_id_clone = event_id.clone();
        let job_topic_clone = job_topic.clone();
        let routing_scope_clone = routing_scope.clone();
        let payload_hex_clone = payload_hex.clone();
        let job_version_str_clone = job_version_str.clone();
        let resource_id_clone = resource_id.clone();
        let payload_schema_version_str_clone = payload_schema_version_str.clone();
        let idle_str_clone = idle_str.clone();

        crate::observability::otel::CURRENT_TRACE_ID.scope(trace_id_hex_clone, async move {
            use opentelemetry::trace::{Tracer, Span, TraceContextExt};

            // 1. Phân tích ngữ cảnh cha (Parent Span) từ traceparent truyền qua WAL
            let cx = if let Some(parent_ctx) = OtelTracer::parse_traceparent(&trace_id_hex) {
                opentelemetry::Context::current().with_remote_span_context(parent_ctx)
            } else {
                opentelemetry::Context::current()
            };

            // 2. Bắt đầu một Span nghiệp vụ mới trong Tempo
            let tracer = opentelemetry::global::tracer("job-proxy");
            let mut span = tracer.start_with_context(format!("cdc.push.{}", job_topic_clone), &cx);

            span.set_attribute(opentelemetry::KeyValue::new("job_id", event_id_clone.clone()));
            span.set_attribute(opentelemetry::KeyValue::new("routing_scope", routing_scope_clone.clone()));

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

            // Phân giải routing_scope để xác định target_zone_id
            let target_zone_id = if routing_scope_clone == "platform" || routing_scope_clone == "global" {
                // Định tuyến ngẫu nhiên tới các zone hoạt động để phân phối tải (Load Balancing)
                match self.resolve_platform_zone().await {
                    Ok(zone) => {
                        zone
                    }
                    Err(e) => {
                        span.record_error(e.as_ref());
                        Logger::sys_error("cdc.insert", "CdcStreamer: Không thể phân giải platform zone", &e.to_string());
                        return Err(e);
                    }
                }
            } else if routing_scope_clone.starts_with("zone:") {
                // Trích xuất zone UUID sau tiền tố 'zone:'
                routing_scope_clone.strip_prefix("zone:").unwrap_or(&routing_scope_clone).to_string()
            } else {
                // Tương thích ngược: Nếu lưu trực tiếp UUID
                routing_scope_clone.clone()
            };

            span.set_attribute(opentelemetry::KeyValue::new("zone_id", target_zone_id.clone()));

            // Định tuyến dynamic stream key theo target_zone_id
            let stream_key = format!("jobs:{}", target_zone_id);

            // Tinh chỉnh tối ưu hóa hiệu năng (HA Performance): 
            // Thay vì bọc JSON gây chi phí Serde và phình to mảng bytes nhị phân,
            // ta đẩy trực tiếp các trường dưới dạng Key-Value thô của Redis Stream.
            // Đẩy trace_id dạng raw binary bytes (16 bytes) thay vì string hex 32 bytes để tiết kiệm 50% dung lượng.
            let mut cmd = redis::cmd("XADD");
            cmd.arg(&stream_key).arg("*");
            cmd.arg("job_id").arg(&event_id_clone);
            cmd.arg("job_version").arg(job_version.to_string());
            cmd.arg("attempt").arg("0");
            cmd.arg("job_topic").arg(&job_topic_clone);
            cmd.arg("resource_id").arg(&resource_id_clone);
            cmd.arg("payload_schema_version").arg(payload_schema_version.to_string());
            cmd.arg("payload").arg(&payload_bytes);
            cmd.arg("trace_id").arg(&trace_id_bytes);
            if let Some(idle_val) = idle {
                cmd.arg("idle").arg(idle_val.to_string());
            }

            // Gửi lệnh XADD sang Redis bất đồng bộ
            let xadd_res: Result<String, redis::RedisError> = cmd.query_async(redis_conn).await;

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

    /// Lấy danh sách các zone active và chọn ngẫu nhiên một zone (có tích hợp caching 30 giây tránh quá tải DB)
    async fn resolve_platform_zone(&self) -> Result<String, Box<dyn std::error::Error>> {
        let mut cache = self.active_zones_cache.lock().await;
        let now = std::time::Instant::now();
        
        let zones = if let Some((ref zones, ref updated_at)) = *cache {
            if now.duration_since(*updated_at) < std::time::Duration::from_secs(30) {
                zones.clone()
            } else {
                let fresh_zones = self.fetch_active_zones_from_db().await?;
                *cache = Some((fresh_zones.clone(), now));
                fresh_zones
            }
        } else {
            let fresh_zones = self.fetch_active_zones_from_db().await?;
            *cache = Some((fresh_zones.clone(), now));
            fresh_zones
        };

        if zones.is_empty() {
            return Err("No active zones found in core.zones".into());
        }

        // Lấy vị trí ngẫu nhiên bằng cách modulo thời gian mili-giây hiện tại (Zero-dependency random)
        let index = (chrono::Utc::now().timestamp_millis() as usize) % zones.len();
        Ok(zones[index].clone())
    }

    /// Truy vấn trực tiếp từ bảng core.zones các zone đang có trạng thái active
    async fn fetch_active_zones_from_db(&self) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        use tokio_postgres::NoTls;
        
        // Thiết lập kết nối tạm thời không TLS
        let (pg_client, connection) = tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error("cdc.postgres", "CdcStreamer: Lỗi luồng kết nối Postgres", &e.to_string());
            }
        });

        // Query danh sách zone_id UUID dưới dạng String
        let rows = pg_client
            .query("SELECT id::text FROM core.zones WHERE status = 'active'", &[])
            .await?;

        let mut active_zones = Vec::new();
        for row in rows {
            let zone_id: String = row.get(0);
            active_zones.push(zone_id);
        }
        
        Ok(active_zones)
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
