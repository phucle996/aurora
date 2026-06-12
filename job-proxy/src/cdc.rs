use std::collections::HashMap;
use crate::config::Config;
use crate::payload::JobPayload;
use tokio_postgres::NoTls;

use crate::logger::Logger;

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

    /// Khởi chạy luồng stream nhận và phân phối sự kiện từ WAL
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info("cdc.run", "CdcStreamer: Bắt đầu kết nối SQL polling tới PostgreSQL...");

        // Kết nối PostgreSQL thông qua thư viện tokio-postgres chuẩn
        let (client, connection) = tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error("cdc.postgres", "CdcStreamer: Lỗi kết nối PostgreSQL", &e.to_string());
            }
        });

        // Đảm bảo search path nằm ở mail schema
        client.execute("SET search_path TO mail, public", &[]).await?;

        // Khởi tạo kết nối Redis Multiplexed Client
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        Logger::sys_info("cdc.run", &format!("CdcStreamer: Đang lắng nghe các thay đổi từ logical slot: {}...", self.config.slot_name));

        let mut interval = tokio::time::interval(tokio::time::Duration::from_millis(100));
        loop {
            interval.tick().await;

            // Đọc các thay đổi mới và tự động xác nhận (acknowledge) để giải phóng WAL slot
            let rows = match client.query(
                "SELECT lsn, xid, data FROM pg_logical_slot_get_changes($1, NULL, 100)",
                &[&self.config.slot_name],
            ).await {
                Ok(rows) => rows,
                Err(err) => {
                    Logger::sys_error("cdc.poll", "CdcStreamer: Lỗi đọc pg_logical_slot_get_changes", &err.to_string());
                    tokio::time::sleep(tokio::time::Duration::from_secs(2)).await;
                    continue;
                }
            };

            for row in rows {
                let data: String = row.get("data");
                if data.contains("table mail.mail_outbox_records: INSERT:") {
                    if let Err(err) = self.process_insert(&data, &mut redis_conn).await {
                        Logger::sys_error("cdc.insert", "CdcStreamer: Lỗi xử lý sự kiện INSERT", &err.to_string());
                    }
                }
            }
        }
    }

    /// Xử lý và parse sự kiện INSERT trên bảng outbox, chuyển tiếp sang Redis Stream jobs:<zone_id>
    async fn process_insert(
        &self,
        data: &str,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        // Parse dòng thay đổi định dạng test_decoding
        let fields = parse_test_decoding(data);

        let event_id = fields.get("event_id").cloned().unwrap_or_default();
        let zone_id = fields.get("zone_id").cloned().unwrap_or_default();
        let job_topic = fields.get("job_topic").cloned().unwrap_or_default();
        let payload_json = fields.get("payload_json").cloned().unwrap_or_default();
        let attempts_str = fields.get("attempts").cloned().unwrap_or_default();
        let job_version_str = fields.get("job_version").cloned().unwrap_or_default();
        let resource_id = fields.get("resource_id").cloned().unwrap_or_default();
        let payload_schema_version_str = fields.get("payload_schema_version").cloned().unwrap_or_default();
        let trace_id = fields.get("trace_id").cloned().unwrap_or_default();
        let idle_str = fields.get("idle").cloned().unwrap_or_default();

        if event_id.is_empty() || zone_id.is_empty() || job_topic.is_empty() {
            Logger::sys_warn("cdc.insert", "CdcStreamer: Bỏ qua dòng insert không hợp lệ", "Missing event_id/zone_id/job_topic");
            return Ok(());
        }

        // Tích hợp tracing: Trích xuất trace_id từ WAL và đưa vào Span Context của task hiện tại
        if !trace_id.is_empty() {
            crate::otel::OtelTracer::inject_trace_context(&trace_id);
        }

        let attempt = attempts_str.parse::<u32>().unwrap_or(0);
        let job_version = job_version_str.parse::<u32>().unwrap_or(1);
        let payload_schema_version = payload_schema_version_str.parse::<u32>().unwrap_or(1);
        let idle = idle_str.parse::<u32>().unwrap_or(30);

        // Đóng gói cấu trúc JobPayload khớp 100% với Dataplane Deserializer contract
        let payload = JobPayload {
            job_id: event_id.clone(),
            job_version,
            attempt,
            job_topic: job_topic.clone(),
            resource_id,
            payload_schema_version,
            payload_json,
            trace_id,
            idle,
        };

        // Serialize gói tin sang JSON string
        let payload_str = serde_json::to_string(&payload)?;

        // Định tuyến stream qua zone_id: jobs:<zone_id>
        let stream_key = format!("jobs:{}", zone_id);

        Logger::job_log(&event_id, &job_topic, attempt, "cdc.push", &format!("Push job sang Redis Stream {}", stream_key));

        // Đẩy tin nhắn vào Redis Stream (Sử dụng XADD)
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

/// Bộ phân tích cú pháp thô chuỗi test_decoding an toàn và tối ưu O(N)
fn parse_test_decoding(data: &str) -> HashMap<String, String> {
    let mut map = HashMap::new();
    let prefix = "table mail.mail_outbox_records: INSERT: ";
    if !data.starts_with(prefix) {
        return map;
    }
    let s = &data[prefix.len()..];
    let bytes = s.as_bytes();
    
    let mut i = 0;
    let mut last_key: Option<String> = None;
    let mut last_val_start = 0;

    while i < bytes.len() {
        if bytes[i].is_ascii_alphabetic() || bytes[i] == b'_' {
            let key_start = i;
            while i < bytes.len() && (bytes[i].is_ascii_alphanumeric() || bytes[i] == b'_') {
                i += 1;
            }
            if i < bytes.len() && bytes[i] == b'[' {
                let type_start = i + 1;
                while i < bytes.len() && bytes[i] != b']' {
                    i += 1;
                }
                if i < bytes.len() && bytes[i] == b']' {
                    i += 1;
                    if i < bytes.len() && bytes[i] == b':' {
                        let key = &s[key_start..type_start - 1];
                        let val_start = i + 1;

                        if let Some(ref k) = last_key {
                            let raw_val = s[last_val_start..key_start].trim();
                            map.insert(k.to_string(), clean_val(raw_val));
                        }

                        last_key = Some(key.to_string());
                        last_val_start = val_start;
                    }
                }
            }
        }
        i += 1;
    }

    if let Some(ref k) = last_key {
        let raw_val = s[last_val_start..].trim();
        map.insert(k.to_string(), clean_val(raw_val));
    }

    map
}

/// Dọn dẹp dấu nháy đơn hoặc giá trị null từ test_decoding
fn clean_val(val: &str) -> String {
    if val == "null" {
        return String::new();
    }
    let val = if val.starts_with('\'') && val.ends_with('\'') && val.len() >= 2 {
        &val[1..val.len() - 1]
    } else {
        val
    };
    val.replace("''", "'")
}
