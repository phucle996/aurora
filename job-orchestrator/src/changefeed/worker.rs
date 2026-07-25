use crate::config::Config;
use crate::infra::kafka::transport_proto::{
    DeadLetterRecordV1, JobCommandV1, ZoneMetadataSnapshotV1, ZoneServiceDesiredStateV1,
};
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::{LogFields, Logger};
use crate::observability::otel::OtelTracer;
use pgwire_replication::{Lsn, ReplicationClient, ReplicationConfig, ReplicationEvent};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::sync::Arc;

use super::connection::parse_pg_config;
use super::pgoutput::{
    parse_insert_message, parse_relation_message, parse_update_message, read_u32, DecodedRow,
    PgOutputRelation,
};

#[derive(Debug)]
struct PermanentChangeError(String);

impl std::fmt::Display for PermanentChangeError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for PermanentChangeError {}

/// ChangefeedWorker duy trì logical replication và chỉ advance LSN sau durable
/// Kafka publication hoặc một terminal outcome đã được phân loại rõ.
pub struct ChangefeedWorker {
    config: Config,
    kafka: Arc<KafkaTransport>,
    /// [COMMENT]: Cache desired_state của từng (zone_id, service_type) — dùng để phát hiện thay đổi thực sự.
    /// Persist qua các lần reconnect (không reset khi replication stream ngắt/reconnect).
    /// Key: (zone_id, service_type), Value: desired_state hiện tại (true = enabled).
    desired_state_cache: std::sync::Mutex<HashMap<(String, String), bool>>,
}

impl ChangefeedWorker {
    /// Khởi tạo worker và bootstrap desired_state_cache từ DB.
    /// Tránh publish spurious Zone snapshots khi replay changefeed sau restart.
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
            "changefeed.cache_bootstrap",
            &format!(
                "ChangefeedWorker: Bootstrap desired_state_cache thành công — {} entries.",
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
            "changefeed.run",
            "ChangefeedWorker: Khởi chạy logical changefeed với cơ chế tự động reconnect...",
        );

        let mut retry_delay = 1_u64;
        loop {
            if let Err(e) = self.run_replication_stream().await {
                let jitter = crate::config::get_node_hostname()
                    .bytes()
                    .fold(0_u64, |sum, value| sum.wrapping_add(u64::from(value)))
                    % 3;
                Logger::sys_error(
                    "changefeed.run",
                    "Changefeed session failed; reconnecting with bounded jitter",
                    &e.to_string(),
                );
                tokio::time::sleep(std::time::Duration::from_secs(retry_delay + jitter)).await;
                retry_delay = (retry_delay * 2).min(30);
            } else {
                Logger::sys_info("changefeed.run", "Changefeed session ended; reconnecting");
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                retry_delay = 1;
            }
        }
    }

    /// Kết nối và chạy stream logical replication cho một phiên kết nối cụ thể.
    async fn run_replication_stream(&self) -> Result<(), Box<dyn std::error::Error>> {
        let (pg_host, pg_port, pg_user, pg_password, pg_db) =
            parse_pg_config(&self.config.database_url)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;

        Logger::sys_info(
            "changefeed.run",
            "Connecting PostgreSQL logical replication stream",
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
            "changefeed.run",
            &format!(
                "Listening on logical replication slot {}",
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
                    let outcome: Result<(), Box<dyn std::error::Error>> = async {
                        match tag {
                            b'R' => {
                                let rel =
                                    parse_relation_message(&data).map_err(PermanentChangeError)?;
                                Logger::sys_info(
                                    "changefeed.relation",
                                    &format!(
                                        "Schema table {}.{} (ID: {}) được cập nhật: {} columns",
                                        rel.schema_name,
                                        rel.relation_name,
                                        rel.relation_id,
                                        rel.columns.len()
                                    ),
                                );
                                relation_map.insert(rel.relation_id, rel);
                                Ok(())
                            }
                            b'I' | b'U' => {
                                let mut offset = 1;
                                let relation_id =
                                    read_u32(&data, &mut offset).map_err(PermanentChangeError)?;
                                let rel = relation_map.get(&relation_id).ok_or_else(|| {
                                    std::io::Error::other(format!(
                                        "relation {relation_id} is unknown; reconnect before ACK"
                                    ))
                                })?;
                                // [COMMENT]: Match đủ schema.table; không nhận nhầm outbox cùng tên ở domain khác.
                                let is_monitored =
                                    self.config.changefeed_sources.iter().any(|source| {
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
                                                // Source schema is authoritative routing metadata;
                                                // never infer the owner domain from job_topic.
                                                self.process_insert(&fields, &rel.schema_name)
                                                    .await?;
                                            }
                                        }
                                        Err(err) => {
                                            return Err(PermanentChangeError(format!(
                                                "pgoutput {} parse failed for {}.{}: {err}",
                                                tag as char, rel.schema_name, rel.relation_name
                                            ))
                                            .into());
                                        }
                                    }
                                }
                                Ok(())
                            }
                            _ => Ok(()),
                        }
                    }
                    .await;

                    match outcome {
                        Ok(()) => {}
                        Err(error) if error.is::<PermanentChangeError>() => {
                            self.quarantine_change(wal_end, tag, &data, &error.to_string())
                                .await?;
                        }
                        // Transient PostgreSQL/Kafka errors retain the current LSN.
                        Err(error) => return Err(error),
                    }
                    client.update_applied_lsn(wal_end);
                }
                ReplicationEvent::KeepAlive {
                    wal_end,
                    reply_requested: true,
                    ..
                } => client.update_applied_lsn(wal_end),
                ReplicationEvent::StoppedAt { reached } => {
                    Logger::sys_warn(
                        "changefeed.run",
                        "Logical replication stopped at LSN",
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
        fields: &DecodedRow,
        source_domain: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = fields.text("event_id").unwrap_or_default().to_string();
        // [COMMENT]: Mail dùng UUID typed trực tiếp; Storage giữ routing_scope theo contract riêng của resource jobs.
        let routing_scope = fields.text("routing_scope").unwrap_or_default().to_string();
        let mail_zone_id = fields.text("zone_id").unwrap_or_default().to_string();
        let job_topic = fields.text("job_topic").unwrap_or_default().to_string();
        let payload_hex = fields.text("payload").unwrap_or_default().to_string();
        let job_version_str = fields.text("job_version").unwrap_or_default().to_string();
        let resource_id = fields.text("resource_id").unwrap_or_default().to_string();
        let payload_schema_version_str = fields
            .text("payload_schema_version")
            .unwrap_or_default()
            .to_string();
        let trace_id_raw = fields.text("trace_id").unwrap_or_default().to_string();
        let trace_id_bytes = decode_pg_bytea(&trace_id_raw).map_err(|error| {
            Box::new(PermanentChangeError(format!(
                "invalid outbox trace_id bytea: {error}"
            ))) as Box<dyn std::error::Error>
        })?;
        if !trace_id_bytes.is_empty() && trace_id_bytes.len() != 16 {
            return Err(Box::new(PermanentChangeError(
                "outbox trace_id must be empty or 16 bytes".to_string(),
            )));
        }
        let trace_id_hex = trace_id_bytes
            .iter()
            .map(|b| format!("{:02x}", b))
            .collect::<String>();
        let idle_str = fields.text("idle").unwrap_or_default().to_string();
        let source_domain = source_domain.trim().to_ascii_uppercase();

        let route_missing = if source_domain == "MAIL" {
            mail_zone_id.is_empty()
        } else {
            routing_scope.is_empty()
        };

        if event_id.is_empty() || route_missing || job_topic.is_empty() || source_domain.is_empty()
        {
            return Err(Box::new(PermanentChangeError(
                "monitored outbox row is missing event_id/zone route/job_topic".to_string(),
            )));
        }
        if !crate::job_topics::is_registered(&source_domain, &job_topic) {
            return Err(Box::new(PermanentChangeError(
                "outbox source_domain and job_topic route is not registered".to_string(),
            )));
        }

        crate::observability::metrics::MetricsManager::inc_wal_records_accepted();

        Logger::job_log_with_fields(
            &event_id,
            &job_topic,
            0,
            "changefeed.recv_wal",
            "WAL_OUTBOX_ACCEPTED",
            "Accepted PostgreSQL outbox WAL record",
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
            let payload_bytes = decode_pg_bytea(&payload_hex_clone).map_err(|error| {
                PermanentChangeError(format!("invalid outbox bytea payload: {error}"))
            })?;
            if payload_bytes.is_empty() {
                return Err(PermanentChangeError("outbox payload is empty".to_string()).into());
            }

            let job_version = job_version_str_clone
                .parse::<u32>()
                .map_err(|_| PermanentChangeError("invalid job_version".to_string()))?;
            if job_version == 0 {
                return Err(
                    PermanentChangeError("job_version must be positive".to_string()).into(),
                );
            }
            let payload_schema_version = payload_schema_version_str_clone
                .parse::<u32>()
                .map_err(|_| PermanentChangeError("invalid payload_schema_version".to_string()))?;
            if payload_schema_version == 0 {
                return Err(PermanentChangeError(
                    "payload_schema_version must be positive".to_string(),
                )
                .into());
            }
            let idle = if idle_str_clone.is_empty() {
                None
            } else {
                Some(
                    idle_str_clone
                        .parse::<u32>()
                        .map_err(|_| PermanentChangeError("invalid idle seconds".to_string()))?,
                )
            };

            // [COMMENT]: Durable runtime commands luôn thuộc đúng một Zone.
            // Platform/global routing không được fallback thành shared topic vì một consumer
            // bất kỳ có thể nhận side effect vốn thuộc Zone khác.
            let target_zone_id = canonical_zone_route(
                &source_domain_clone,
                &routing_scope_clone,
                &mail_zone_id_clone,
            )
            .map_err(PermanentChangeError)?;
            let topic = self.kafka.zone_command_topic(&target_zone_id);
            {
                use opentelemetry::trace::TraceContextExt;
                opentelemetry::Context::current().span().set_attribute(
                    opentelemetry::KeyValue::new("aurora.zone.id", target_zone_id.clone()),
                );
            }

            let event_uuid = uuid::Uuid::parse_str(&event_id_clone).map_err(|error| {
                PermanentChangeError(format!("invalid outbox event_id: {error}"))
            })?;
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
                        "changefeed.push_success",
                        "KAFKA_COMMAND_PUBLISHED",
                        &format!("Published job command to Kafka topic {topic}"),
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

    async fn quarantine_change(
        &self,
        wal_end: Lsn,
        tag: u8,
        data: &[u8],
        error: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let payload_hash = format!("{:x}", Sha256::digest(data));
        let identity = format!("{}:{tag}:{payload_hash}", wal_end.0);
        let event_id = uuid::Uuid::new_v5(&uuid::Uuid::NAMESPACE_OID, identity.as_bytes());
        let record = DeadLetterRecordV1 {
            event_id: event_id.as_bytes().to_vec(),
            source_topic: "postgres.logical_changefeed".to_string(),
            source_partition: 0,
            source_offset: i64::try_from(wal_end.0).unwrap_or(i64::MAX),
            error_code: "CHANGEFEED_RECORD_INVALID".to_string(),
            error_message: format!(
                "lsn={} tag={} payload_len={} sha256={} error={}",
                wal_end,
                char::from(tag),
                data.len(),
                payload_hash,
                bounded_utf8(error, 256)
            ),
            // WAL row payloads can contain encrypted envelopes or credentials.
            // Quarantine keeps only a fingerprint, never a second raw copy.
            original_payload: Vec::new(),
            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            schema_version: 1,
        };
        self.kafka
            .publish_message(
                &self.kafka.dead_letter_topic(),
                event_id.as_bytes(),
                &record,
            )
            .await
            .map_err(std::io::Error::other)?;
        crate::observability::metrics::MetricsManager::record_wal_rejected();
        crate::observability::metrics::MetricsManager::record_dlq_published();
        let event_id_text = event_id.to_string();
        Logger::sys_warn_with_fields(
            "changefeed.quarantine",
            "CHANGEFEED_RECORD_INVALID",
            "Permanent WAL error was durably quarantined before LSN advance",
            "",
            LogFields {
                event_id: Some(&event_id_text),
                outcome: Some("quarantined"),
                ..LogFields::default()
            },
        );
        Ok(())
    }

    /// Xử lý changefeed cấu hình từ bảng zones và zone_services.
    /// Với zone_services: chỉ publish khi desired_state THỰC SỰ THAY ĐỔI so với cache.
    /// Cache được bootstrap từ DB lúc startup và cập nhật sau mỗi publish — tránh spurious events.
    async fn process_zone_config_change(
        &self,
        fields: &DecodedRow,
        table_name: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let zone_id = if table_name == "zones" {
            fields.text("id").unwrap_or_default().to_string()
        } else {
            fields.text("zone_id").unwrap_or_default().to_string()
        };
        if uuid::Uuid::parse_str(&zone_id).is_err() {
            return Err(Box::new(PermanentChangeError(
                "monitored Zone change has an invalid zone UUID".to_string(),
            )));
        }

        let mut cache_update = None;
        if table_name == "zone_services" {
            let service_type = fields.text("service_type").unwrap_or_default().to_string();
            let enabled = fields
                .text("desired_state")
                .is_some_and(|value| value == "t" || value == "true");
            let key = (zone_id.clone(), service_type.clone());
            if self
                .desired_state_cache
                .lock()
                .map_err(|_| "desired-state cache lock poisoned")?
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
                .map_err(|_| "desired-state cache lock poisoned")?
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
    if !hex_part.len().is_multiple_of(2) {
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

fn bounded_utf8(value: &str, max_bytes: usize) -> String {
    if value.len() <= max_bytes {
        return value.to_string();
    }
    let mut end = max_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_string()
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
