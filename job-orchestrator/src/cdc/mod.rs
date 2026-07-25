pub mod parser;
pub mod setup;
pub mod utils;
use crate::config::Config;
use crate::infra::kafka::transport_proto::{
    JobCommandV1, ZoneMetadataSnapshotV1, ZoneServiceDesiredStateV1,
};
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::{LogFields, Logger};
use crate::observability::otel::OtelTracer;
use pgwire_replication::{Lsn, ReplicationClient, ReplicationConfig, ReplicationEvent};
use std::collections::HashMap;
use std::sync::Arc;

use parser::{
    parse_insert_message, parse_relation_message, parse_update_message, read_u32, PgOutputRelation,
};
use utils::parse_pg_config;

/// CdcStreamer chịu trách nhiệm kết nối và duy trì luồng stream logical replication từ PostgreSQL.
pub struct CdcStreamer {
    config: Config,
    kafka: Arc<KafkaTransport>,
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
        kafka: Arc<KafkaTransport>,
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
            kafka,
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
                                    // [COMMENT]: Match đủ schema.table; không nhận nhầm outbox cùng tên ở domain khác.
                                    let is_monitored =
                                        self.config.cdc_sources.iter().any(|source| {
                                            if let Some((schema_name, table_name)) =
                                                source.split_once('.')
                                            {
                                                rel.schema_name == schema_name
                                                    && rel.relation_name == table_name
                                            } else {
                                                rel.relation_name == source.as_str()
                                            }
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
                                                    )
                                                    .await?;
                                                } else if tag == b'I' {
                                                    // [COMMENT]: Source schema là CDC metadata; không suy diễn owner domain từ job_topic.
                                                    self.process_insert(&fields, &rel.schema_name)
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

    /// Xử lý sự kiện INSERT đã giải mã, định tuyến và publish Protobuf sang Kafka.
    async fn process_insert(
        &self,
        fields: &HashMap<String, String>,
        source_domain: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = fields.get("event_id").cloned().unwrap_or_default();
        // [COMMENT]: Mail dùng UUID typed trực tiếp; Storage giữ routing_scope theo contract riêng của resource jobs.
        let routing_scope = fields.get("routing_scope").cloned().unwrap_or_default();
        let mail_zone_id = fields.get("zone_id").cloned().unwrap_or_default();
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

        let route_missing = if source_domain == "MAIL" {
            mail_zone_id.is_empty()
        } else {
            routing_scope.is_empty()
        };

        if event_id.is_empty() || route_missing || job_topic.is_empty() || source_domain.is_empty()
        {
            crate::observability::metrics::MetricsManager::record_wal_rejected();
            Logger::sys_warn(
                "cdc.insert",
                "CdcStreamer: Bỏ qua dòng insert thiếu trường quan trọng",
                "Missing event_id/zone route/job_topic",
            );
            return Ok(());
        }

        crate::observability::metrics::MetricsManager::inc_wal_records_accepted();

        Logger::job_log_with_fields(
            &event_id,
            &job_topic,
            0,
            "cdc.recv_wal",
            "WAL_OUTBOX_ACCEPTED",
            "CdcStreamer: Nhận được sự kiện WAL từ Postgres",
            LogFields {
                event_id: Some(&event_id),
                source_domain: Some(&source_domain),
                job_version: job_version_str.parse::<u64>().ok(),
                outcome: Some("accepted"),
                ..LogFields::default()
            },
        );

        let event_id_clone = event_id.clone();
        let job_topic_clone = job_topic.clone();
        let routing_scope_clone = routing_scope.clone();
        let mail_zone_id_clone = mail_zone_id.clone();
        let payload_hex_clone = payload_hex.clone();
        let job_version_str_clone = job_version_str.clone();
        let resource_id_clone = resource_id.clone();
        let payload_schema_version_str_clone = payload_schema_version_str.clone();
        let idle_str_clone = idle_str.clone();
        let source_domain_clone = source_domain.clone();
        let producer_context = OtelTracer::start_span_with_parent(
            format!("send {}", job_topic_clone),
            opentelemetry::trace::SpanKind::Producer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "kafka"),
                opentelemetry::KeyValue::new("messaging.operation.type", "send"),
                opentelemetry::KeyValue::new("aurora.job.id", event_id_clone.clone()),
                opentelemetry::KeyValue::new("aurora.job.topic", job_topic_clone.clone()),
                // [COMMENT]: PostgreSQL chỉ lưu correlation trace ID legacy. Nó không
                // được giả làm remote parent vì thiếu parent span và sampling flags.
                opentelemetry::KeyValue::new("aurora.legacy_trace_id", trace_id_hex),
            ],
            &opentelemetry::Context::new(),
        );
        let propagation = OtelTracer::inject_context(&producer_context);

        use opentelemetry::trace::FutureExt;
        let publish_result: Result<(), Box<dyn std::error::Error>> = async move {
            let payload_bytes = match decode_pg_bytea(&payload_hex_clone) {
                Ok(bytes) => bytes,
                Err(error) => return Err(error),
            };

            let job_version = job_version_str_clone.parse::<u32>().unwrap_or(1);
            let payload_schema_version =
                payload_schema_version_str_clone.parse::<u32>().unwrap_or(1);
            let idle = if idle_str_clone.is_empty() {
                None
            } else {
                idle_str_clone.parse::<u32>().ok()
            };

            // [COMMENT]: Durable runtime commands luôn thuộc đúng một Zone.
            // Platform/global routing không được fallback thành shared topic vì một consumer
            // bất kỳ có thể nhận side effect vốn thuộc Zone khác.
            let target_zone_id = canonical_zone_route(
                &source_domain_clone,
                &routing_scope_clone,
                &mail_zone_id_clone,
            )
            .map_err(std::io::Error::other)?;
            let topic = self.kafka.zone_command_topic(&target_zone_id);
            {
                use opentelemetry::trace::TraceContextExt;
                opentelemetry::Context::current().span().set_attribute(
                    opentelemetry::KeyValue::new("aurora.zone.id", target_zone_id.clone()),
                );
            }

            let event_uuid = uuid::Uuid::parse_str(&event_id_clone)
                .map_err(|error| format!("invalid outbox event_id: {error}"))?;
            let command = JobCommandV1 {
                job_id: event_uuid.as_bytes().to_vec(),
                job_version,
                attempt: 0,
                job_topic: job_topic_clone.clone(),
                source_domain: source_domain_clone.clone(),
                resource_id: resource_id_clone,
                payload_schema_version,
                payload: payload_bytes,
                trace_id: trace_id_bytes,
                idle_seconds: idle,
                reconcile_generation: None,
                target_zone_id,
                transport_schema_version: 1,
                traceparent: propagation.traceparent,
                tracestate: propagation.tracestate,
            };

            match self
                .kafka
                .publish_message(&topic, event_uuid.as_bytes(), &command)
                .await
            {
                Ok(()) => {
                    Logger::job_log_with_fields(
                        &event_id_clone,
                        &job_topic_clone,
                        0,
                        "cdc.push_success",
                        "KAFKA_COMMAND_PUBLISHED",
                        &format!("CdcStreamer: Đã publish job Protobuf vào Kafka {topic}"),
                        LogFields {
                            event_id: Some(&event_id_clone),
                            source_domain: Some(&source_domain_clone),
                            job_version: Some(u64::from(job_version)),
                            kafka_topic: Some(&topic),
                            outcome: Some("published"),
                            ..LogFields::default()
                        },
                    );
                    crate::observability::metrics::MetricsManager::inc_kafka_commands_published();
                }
                Err(error) => {
                    return Err(std::io::Error::other(error).into());
                }
            }

            Ok(())
        }
        .with_context(producer_context.clone())
        .await;
        OtelTracer::finish_span(
            &producer_context,
            publish_result
                .as_ref()
                .err()
                .map(|_| "KAFKA_COMMAND_PUBLISH_FAILED"),
        );
        publish_result?;

        Ok(())
    }

    /// Xử lý CDC thay đổi cấu hình từ bảng zones và zone_services.
    /// Với zone_services: chỉ publish khi desired_state THỰC SỰ THAY ĐỔI so với cache.
    /// Cache được bootstrap từ DB lúc startup và cập nhật sau mỗi publish — tránh spurious events.
    async fn process_zone_config_change(
        &self,
        fields: &HashMap<String, String>,
        table_name: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let zone_id = if table_name == "zones" {
            fields.get("id").cloned().unwrap_or_default()
        } else {
            fields.get("zone_id").cloned().unwrap_or_default()
        };
        if uuid::Uuid::parse_str(&zone_id).is_err() {
            return Ok(());
        }

        let mut cache_update = None;
        if table_name == "zone_services" {
            let service_type = fields.get("service_type").cloned().unwrap_or_default();
            let enabled = fields
                .get("desired_state")
                .is_some_and(|value| value == "t" || value == "true");
            let key = (zone_id.clone(), service_type.clone());
            if self
                .desired_state_cache
                .lock()
                .unwrap()
                .get(&key)
                .is_some_and(|cached| *cached == enabled)
            {
                return Ok(());
            }
            cache_update = Some((key, enabled));
        }

        // [COMMENT]: Luôn publish full snapshot; compacted topic không phụ thuộc thứ tự delta khi pod cold-start.
        let (status, services) = crate::reverse_provider::zone::db::query_zone_metadata(
            &self.config.database_url,
            &zone_id,
        )
        .await?;
        let event_id = uuid::Uuid::new_v4();
        let snapshot = ZoneMetadataSnapshotV1 {
            event_id: event_id.as_bytes().to_vec(),
            zone_id: uuid::Uuid::parse_str(&zone_id)?.as_bytes().to_vec(),
            status,
            services: services
                .into_iter()
                .map(|(service_type, enabled)| ZoneServiceDesiredStateV1 {
                    service_type,
                    enabled,
                })
                .collect(),
            observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            schema_version: 1,
        };
        self.kafka
            .publish_message(
                &self.kafka.metadata_topic(&zone_id),
                zone_id.as_bytes(),
                &snapshot,
            )
            .await
            .map_err(std::io::Error::other)?;

        // [COMMENT]: Cache chỉ tiến sau acks=all; publish lỗi sẽ để WAL replay thử lại.
        if let Some((key, enabled)) = cache_update {
            self.desired_state_cache
                .lock()
                .unwrap()
                .insert(key, enabled);
        }

        Ok(())
    }
}

fn canonical_zone_route(
    source_domain: &str,
    routing_scope: &str,
    mail_zone_id: &str,
) -> Result<String, String> {
    let raw_zone_id = if source_domain == "MAIL" {
        mail_zone_id
    } else {
        routing_scope.strip_prefix("zone:").unwrap_or(routing_scope)
    };
    let parsed_zone_id = uuid::Uuid::parse_str(raw_zone_id)
        .map_err(|error| format!("runtime command requires a valid zone UUID: {error}"))?;
    if parsed_zone_id.is_nil() {
        return Err("runtime command requires a non-nil zone UUID".to_string());
    }
    Ok(parsed_zone_id.to_string())
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

#[cfg(test)]
mod tests {
    use super::canonical_zone_route;

    const ZONE_ID: &str = "019f3d3e-997d-7894-9236-c5122634cb4f";

    #[test]
    fn canonical_zone_route_accepts_mail_and_scoped_storage_routes() {
        assert_eq!(canonical_zone_route("MAIL", "", ZONE_ID).unwrap(), ZONE_ID);
        assert_eq!(
            canonical_zone_route("STORAGE", &format!("zone:{ZONE_ID}"), "").unwrap(),
            ZONE_ID
        );
    }

    #[test]
    fn canonical_zone_route_rejects_shared_and_nil_routes() {
        assert!(canonical_zone_route("STORAGE", "platform", "").is_err());
        assert!(canonical_zone_route("STORAGE", "global", "").is_err());
        assert!(canonical_zone_route("MAIL", "", "00000000-0000-0000-0000-000000000000").is_err());
    }
}
