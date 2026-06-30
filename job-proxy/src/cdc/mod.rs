pub mod parser;
pub mod setup;
pub mod utils;
use crate::config::Config;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use pgwire_replication::{Lsn, ReplicationClient, ReplicationConfig, ReplicationEvent};
use std::collections::HashMap;

use parser::{parse_insert_message, parse_relation_message, read_u32, PgOutputRelation};
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
                        b'I' => {
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
                                        let fields = parse_insert_message(&data, &rel.columns)
                                            .map_err(|e| {
                                                std::io::Error::new(
                                                    std::io::ErrorKind::InvalidData,
                                                    e,
                                                )
                                            })?;

                                        self.process_insert(&fields, &mut redis_conn).await?;
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
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
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

        if event_id.is_empty() || routing_scope.is_empty() || job_topic.is_empty() {
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

                if job_topic_clone == "zone.metadata.update" {
                    // Đây là sự kiện cấu hình được trigger tự động ghi nhận
                    // Publish nhị phân trực tiếp lên kênh PubSub Platform: zone:event:metadata:<zone_id>
                    let channel = format!("zone:event:metadata:{}", resource_id_clone);
                    let publish_res: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
                        .arg(&channel)
                        .arg(&payload_bytes)
                        .query_async(redis_conn)
                        .await;

                    match publish_res {
                        Ok(_) => {
                            Logger::sys_info(
                                "cdc.publish_config",
                                &format!("CdcStreamer: Đã phát tán CDC Metadata update lên kênh PubSub {}", channel)
                            );
                        }
                        Err(e) => {
                            span.record_error(&e);
                            return Err(Box::new(e) as Box<dyn std::error::Error>);
                        }
                    }
                    return Ok(());
                }

                let mut cmd = redis::cmd("XADD");
                cmd.arg(&stream_key).arg("*");
                cmd.arg("job_id").arg(&event_id_clone);
                cmd.arg("job_version").arg(job_version.to_string());
                cmd.arg("attempt").arg("0");
                cmd.arg("job_topic").arg(&job_topic_clone);
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
