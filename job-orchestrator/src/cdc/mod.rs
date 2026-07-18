pub mod parser;
pub mod setup;
pub mod utils;
use crate::config::Config;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use pgwire_replication::{Lsn, ReplicationClient, ReplicationConfig, ReplicationEvent};
use std::collections::HashMap;

use parser::{
    parse_insert_message, parse_relation_message, parse_update_message, read_u32, PgOutputRelation,
};
use utils::parse_pg_config;

/// CdcStreamer chịu trách nhiệm kết nối và duy trì luồng stream logical replication từ PostgreSQL.
pub struct CdcStreamer {
    config: Config,
    redis_client: redis::Client,
    /// [COMMENT]: Cache desired_state của từng (zone_id, service_type) — dùng để phát hiện thay đổi thực sự.
    /// Persist qua các lần reconnect (không reset khi replication stream ngắt/reconnect).
    /// Key: (zone_id, service_type), Value: desired_state hiện tại (true = enabled).
    desired_state_cache: std::sync::Mutex<HashMap<(String, String), bool>>,
}

impl CdcStreamer {
    /// Khởi tạo một CdcStreamer mới, bootstrap desired_state_cache từ DB.
    /// Đảm bảo CDC không publish spurious events cho các service đã ở trạng thái đúng khi startup.
    pub async fn new(
        config: Config,
        redis_client: redis::Client,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        // [COMMENT]: Bootstrap snapshot từ DB để khởi tạo cache trước khi nhận WAL events.
        // Tránh publish false-positive khi JO restart và WAL replay các event cũ.
        let snapshot = crate::reverse_provider::zone::db::query_all_zone_services_enabled(
            &config.database_url,
        )
        .await?;

        // [COMMENT]: Flatten từ HashMap<zone_id, HashMap<svc_type, bool>>
        // sang HashMap<(zone_id, svc_type), bool> để lookup O(1).
        let mut cache: HashMap<(String, String), bool> = HashMap::new();
        for (zone_id, services) in snapshot {
            for (svc_type, enabled) in services {
                cache.insert((zone_id.clone(), svc_type), enabled);
            }
        }

        Logger::sys_info(
            "cdc.cache_bootstrap",
            &format!(
                "CdcStreamer: Bootstrap desired_state_cache thành công — {} entries.",
                cache.len()
            ),
        );

        Ok(Self {
            config,
            redis_client,
            desired_state_cache: std::sync::Mutex::new(cache),
        })
    }

    /// Khởi chạy luồng stream nhận và phân phối sự kiện từ WAL theo giao thức push-based.
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "cdc.run",
            "CdcStreamer: Khởi chạy luồng giám sát CDC Outbox với cơ chế tự động reconnect...",
        );

        loop {
            if let Err(e) = self.run_replication_stream().await {
                Logger::sys_error(
                    "cdc.run",
                    "CdcStreamer: Gặp lỗi trong luồng replication stream. Tiến hành reconnect sau 5 giây...",
                    &e.to_string()
                );
                tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            } else {
                Logger::sys_info(
                    "cdc.run",
                    "CdcStreamer: Luồng replication kết thúc bình thường. Tiến hành reconnect...",
                );
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
            }
        }
    }

    /// Kết nối và chạy stream logical replication cho một phiên kết nối cụ thể.
    async fn run_replication_stream(&self) -> Result<(), Box<dyn std::error::Error>> {
        let (pg_host, pg_port, pg_user, pg_password, pg_db) =
            parse_pg_config(&self.config.database_url)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;

        Logger::sys_info(
            "cdc.run",
            "CdcStreamer: Tiến hành kết nối stream logical replication...",
        );

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

        let mut client = ReplicationClient::connect(config).await?;
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;
        let mut relation_map: HashMap<u32, PgOutputRelation> = HashMap::new();

        Logger::sys_info(
            "cdc.run",
            &format!(
                "CdcStreamer: Đang lắng nghe thay đổi từ logical slot: {}...",
                self.config.slot_name
            ),
        );

        while let Some(event) = client.recv().await? {
            match event {
                ReplicationEvent::XLogData { wal_end, data, .. } => {
                    if data.is_empty() {
                        client.update_applied_lsn(wal_end);
                        continue;
                    }

                    let tag = data[0];
                    match tag {
                        b'R' => match parse_relation_message(&data) {
                            Ok(rel) => {
                                Logger::sys_info(
                                    "cdc.relation",
                                    &format!(
                                        "Schema table {}.{} (ID: {}) được cập nhật: {} columns",
                                        rel.schema_name,
                                        rel.relation_name,
                                        rel.relation_id,
                                        rel.columns.len()
                                    ),
                                );
                                relation_map.insert(rel.relation_id, rel);
                            }
                            Err(err) => {
                                Logger::sys_error(
                                    "cdc.relation",
                                    "Lỗi phân tích Relation message",
                                    &err,
                                );
                            }
                        },
                        b'I' | b'U' => {
                            let mut offset = 1;
                            if let Ok(relation_id) = read_u32(&data, &mut offset) {
                                if let Some(rel) = relation_map.get(&relation_id) {
                                    let is_monitored =
                                        self.config.cdc_sources.iter().any(|source| {
                                            let parts: Vec<&str> = source.split('.').collect();
                                            let table_name =
                                                if parts.len() == 2 { parts[1] } else { parts[0] };
                                            rel.relation_name == table_name
                                        });

                                    if is_monitored {
                                        let fields_res = if tag == b'I' {
                                            parse_insert_message(&data, &rel.columns)
                                        } else {
                                            parse_update_message(&data, &rel.columns)
                                        };

                                        match fields_res {
                                            Ok(fields) => {
                                                if rel.relation_name == "zones"
                                                    || rel.relation_name == "zone_services"
                                                {
                                                    // [COMMENT]: Bỏ tham số tag — so sánh bằng cache thay vì heuristic.
                                                    self.process_zone_config_change(
                                                        &fields,
                                                        &rel.relation_name,
                                                        &mut redis_conn,
                                                    )
                                                    .await?;
                                                } else if tag == b'I' {
                                                    // [COMMENT]: Source schema là CDC metadata; không suy diễn owner domain từ job_topic.
                                                    self.process_insert(
                                                        &fields,
                                                        &rel.schema_name,
                                                        &mut redis_conn,
                                                    )
                                                    .await?;
                                                }
                                            }
                                            Err(err) => {
                                                Logger::sys_error(
                                                    "cdc.parse_error",
                                                    &format!("Lỗi phân tích message tag '{}' cho bảng {}", tag as char, rel.relation_name),
                                                    &err,
                                                );
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        _ => {}
                    }

                    client.update_applied_lsn(wal_end);
                }
                ReplicationEvent::KeepAlive {
                    wal_end,
                    reply_requested,
                    ..
                } => {
                    if reply_requested {
                        client.update_applied_lsn(wal_end);
                    }
                }
                ReplicationEvent::StoppedAt { reached } => {
                    Logger::sys_warn(
                        "cdc.run",
                        "CdcStreamer: Dừng stream WAL tại LSN",
                        &reached.to_string(),
                    );
                    break;
                }
                _ => {}
            }
        }

        Ok(())
    }

    /// Xử lý sự kiện INSERT đã giải mã, định tuyến và push sang Redis Stream
    async fn process_insert(
        &self,
        fields: &HashMap<String, String>,
        source_domain: &str,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if source_domain.eq_ignore_ascii_case("iam") {
            // [COMMENT]: IAM mail do lease dispatcher DB-poll sở hữu; bỏ direct XADD để một source có đúng một publisher.
            return Ok(());
        }
        let event_id = fields.get("event_id").cloned().unwrap_or_default();
        let routing_scope = fields.get("routing_scope").cloned().unwrap_or_default();
        let job_topic = fields.get("job_topic").cloned().unwrap_or_default();
        let payload_hex = fields.get("payload").cloned().unwrap_or_default();
        let job_version_str = fields.get("job_version").cloned().unwrap_or_default();
        let resource_id = fields.get("resource_id").cloned().unwrap_or_default();
        let payload_schema_version_str = fields
            .get("payload_schema_version")
            .cloned()
            .unwrap_or_default();
        let trace_id_raw = fields.get("trace_id").cloned().unwrap_or_default();
        let trace_id_bytes = decode_pg_bytea(&trace_id_raw).unwrap_or_default();
        let trace_id_hex = trace_id_bytes
            .iter()
            .map(|b| format!("{:02x}", b))
            .collect::<String>();
        let idle_str = fields.get("idle").cloned().unwrap_or_default();
        let source_domain = source_domain.trim().to_ascii_uppercase();

        if event_id.is_empty()
            || routing_scope.is_empty()
            || job_topic.is_empty()
            || source_domain.is_empty()
        {
            Logger::sys_warn(
                "cdc.insert",
                "CdcStreamer: Bỏ qua dòng insert thiếu trường quan trọng",
                "Missing event_id/routing_scope/job_topic",
            );
            return Ok(());
        }

        crate::observability::metrics::MetricsManager::inc_wal_records_read();

        Logger::job_log(
            &event_id,
            &job_topic,
            0,
            "cdc.recv_wal",
            "CdcStreamer: Nhận được sự kiện WAL từ Postgres",
        );

        let trace_id_hex_clone = trace_id_hex.clone();
        let event_id_clone = event_id.clone();
        let job_topic_clone = job_topic.clone();
        let routing_scope_clone = routing_scope.clone();
        let payload_hex_clone = payload_hex.clone();
        let job_version_str_clone = job_version_str.clone();
        let resource_id_clone = resource_id.clone();
        let payload_schema_version_str_clone = payload_schema_version_str.clone();
        let idle_str_clone = idle_str.clone();
        let source_domain_clone = source_domain.clone();

        crate::observability::otel::CURRENT_TRACE_ID
            .scope(trace_id_hex_clone, async move {
                use opentelemetry::trace::{Span, TraceContextExt, Tracer};

                let cx = if let Some(parent_ctx) = OtelTracer::parse_traceparent(&trace_id_hex) {
                    opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                } else {
                    opentelemetry::Context::current()
                };

                let tracer = opentelemetry::global::tracer("job-proxy");
                let mut span =
                    tracer.start_with_context(format!("cdc.push.{}", job_topic_clone), &cx);

                span.set_attribute(opentelemetry::KeyValue::new(
                    "job_id",
                    event_id_clone.clone(),
                ));
                span.set_attribute(opentelemetry::KeyValue::new(
                    "routing_scope",
                    routing_scope_clone.clone(),
                ));

                let payload_bytes = match decode_pg_bytea(&payload_hex_clone) {
                    Ok(bytes) => bytes,
                    Err(e) => {
                        span.record_error(e.as_ref());
                        return Err(e);
                    }
                };

                let job_version = job_version_str_clone.parse::<u32>().unwrap_or(1);
                let payload_schema_version =
                    payload_schema_version_str_clone.parse::<u32>().unwrap_or(1);
                let idle = if idle_str_clone.is_empty() {
                    None
                } else {
                    idle_str_clone.parse::<u32>().ok()
                };

                let (stream_key, target_zone_id) =
                    if routing_scope_clone == "platform" || routing_scope_clone == "global" {
                        ("jobs:platform".to_string(), "platform".to_string())
                    } else {
                        let zone_id = if routing_scope_clone.starts_with("zone:") {
                            routing_scope_clone
                                .strip_prefix("zone:")
                                .unwrap_or(&routing_scope_clone)
                                .to_string()
                        } else {
                            routing_scope_clone.clone()
                        };
                        (format!("jobs:{}", zone_id), zone_id)
                    };

                span.set_attribute(opentelemetry::KeyValue::new("zone_id", target_zone_id));

                let mut cmd = redis::cmd("XADD");
                cmd.arg(&stream_key).arg("*");
                cmd.arg("job_id").arg(&event_id_clone);
                cmd.arg("job_version").arg(job_version.to_string());
                cmd.arg("attempt").arg("0");
                cmd.arg("job_topic").arg(&job_topic_clone);
                // [COMMENT]: Dataplane echo field này trong mọi result để JO update đúng outbox nguồn.
                cmd.arg("source_domain").arg(&source_domain_clone);
                cmd.arg("resource_id").arg(&resource_id_clone);
                cmd.arg("payload_schema_version")
                    .arg(payload_schema_version.to_string());
                cmd.arg("payload").arg(&payload_bytes);
                cmd.arg("trace_id").arg(&trace_id_bytes);
                if let Some(idle_val) = idle {
                    cmd.arg("idle").arg(idle_val.to_string());
                }

                let xadd_res: Result<String, redis::RedisError> = cmd.query_async(redis_conn).await;

                match xadd_res {
                    Ok(_) => {
                        Logger::job_log(
                            &event_id_clone,
                            &job_topic_clone,
                            0,
                            "cdc.push_success",
                            &format!(
                                "CdcStreamer: Đã đẩy thành công job vào Redis Stream {}",
                                stream_key
                            ),
                        );
                        crate::observability::metrics::MetricsManager::inc_stream_jobs_pushed();
                    }
                    Err(e) => {
                        span.record_error(&e);
                        return Err(Box::new(e) as Box<dyn std::error::Error>);
                    }
                }

                Ok(())
            })
            .await?;

        Ok(())
    }

    /// Xử lý CDC thay đổi cấu hình từ bảng zones và zone_services.
    /// Với zone_services: chỉ publish khi desired_state THỰC SỰ THAY ĐỔI so với cache.
    /// Cache được bootstrap từ DB lúc startup và cập nhật sau mỗi publish — tránh spurious events.
    async fn process_zone_config_change(
        &self,
        fields: &HashMap<String, String>,
        table_name: &str,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut zone_id = String::new();
        let mut event_payload = serde_json::Value::Null;

        if table_name == "zones" {
            // [COMMENT]: zone_status thay đổi → luôn publish (không cần cache so sánh
            // vì zone_status không bị ghi thường xuyên như actual_state của service).
            zone_id = fields.get("id").cloned().unwrap_or_default();
            let status = fields.get("status").cloned().unwrap_or_default();
            if !zone_id.is_empty() && !status.is_empty() {
                event_payload = serde_json::json!({
                    "event_type": "zone_status_changed",
                    "zone_id": zone_id,
                    "status": status
                });
            }
        } else if table_name == "zone_services" {
            zone_id = fields.get("zone_id").cloned().unwrap_or_default();
            let service_type = fields.get("service_type").cloned().unwrap_or_default();
            let desired_state_raw = fields.get("desired_state").cloned().unwrap_or_default();

            if !zone_id.is_empty() && !service_type.is_empty() {
                let new_enabled = desired_state_raw == "t" || desired_state_raw == "true";
                let cache_key = (zone_id.clone(), service_type.clone());

                // [COMMENT]: So sánh desired_state mới với giá trị đang cache.
                // Chỉ publish khi thực sự thay đổi — bất kể WAL event đến từ SRE hay JO.
                // JO chỉ UPDATE actual_state (pure UPDATE) nhưng WAL vẫn chứa desired_state row hiện tại.
                // Vì desired_state không đổi → so sánh == cached → bỏ qua → không spam.
                let should_publish = {
                    let cache = self.desired_state_cache.lock().unwrap();
                    match cache.get(&cache_key) {
                        // [COMMENT]: Nếu cache chưa có entry (zone mới tạo sau bootstrap) → publish lần đầu
                        None => true,
                        // [COMMENT]: Chỉ publish khi desired_state thực sự khác cached value
                        Some(&cached_enabled) => cached_enabled != new_enabled,
                    }
                };

                if should_publish {
                    // [COMMENT]: Cập nhật cache TRƯỚC khi publish để tránh double-publish
                    // nếu publish thất bại và được retry (idempotent cache update).
                    {
                        let mut cache = self.desired_state_cache.lock().unwrap();
                        cache.insert(cache_key, new_enabled);
                    }

                    event_payload = serde_json::json!({
                        "event_type": "service_status_changed",
                        "zone_id": zone_id,
                        "service": service_type,
                        "enabled": new_enabled
                    });
                } else {
                    // [COMMENT]: desired_state không đổi → bỏ qua silently.
                    // Đây là trường hợp JO ghi actual_state → WAL chứa desired_state giống cache.
                    return Ok(());
                }
            }
        }

        if !zone_id.is_empty() && !event_payload.is_null() {
            let channel = format!("zone:event:metadata:{}", zone_id);
            if let Ok(payload_bin) = serde_json::to_vec(&event_payload) {
                let publish_res: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
                    .arg(&channel)
                    .arg(&payload_bin[..])
                    .query_async(redis_conn)
                    .await;

                match publish_res {
                    Ok(_) => {
                        Logger::sys_info(
                            "cdc.publish_zone_config",
                            &format!(
                                "CdcStreamer: Publish CDC zone_services thay đổi trên kênh {} — desired_state changed",
                                channel
                            ),
                        );
                    }
                    Err(e) => {
                        Logger::sys_error(
                            "cdc.publish_zone_config_error",
                            &format!(
                                "Thất bại khi gửi CDC cập nhật cấu hình cho zone {}",
                                zone_id
                            ),
                            &e.to_string(),
                        );
                    }
                }
            }
        }

        Ok(())
    }
}

/// Giải mã chuỗi hex biểu diễn cột BYTEA trong replication message của Postgres
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
