use super::pgoutput::DecodedRow;
use super::quarantine::{canonical_zone_route, decode_pg_bytea};
use super::worker::{ChangefeedWorker, PermanentChangeError};
use crate::infra::kafka::transport_proto::{JobCommandV1, PayloadEncodingV1, ProtectedPayloadV1};
use crate::observability::logger::{LogFields, Logger};
use crate::observability::otel::OtelTracer;
use pgwire_replication::Lsn;
use prost::Message;

#[derive(Debug, Eq, PartialEq)]
enum ManagedServiceFenceDecision {
    Publish,
    Stale,
}

struct ManagedServiceAuthoritativeFence {
    zone_id: uuid::Uuid,
    job_topic: String,
    job_version: i32,
    delivery_epoch: i64,
    status: String,
}

struct ManagedServiceWalFence<'a> {
    zone_id: uuid::Uuid,
    job_topic: &'a str,
    job_version: i32,
    delivery_epoch: i64,
}

struct DurableOutboxCommand {
    event_id: uuid::Uuid,
    job_version: u32,
    job_topic: String,
    source_domain: String,
    resource_id: String,
    payload_schema_version: u32,
    protected_payload: Vec<u8>,
    trace_id: Vec<u8>,
    idle_seconds: Option<u32>,
    target_zone_id: String,
    traceparent: String,
    tracestate: String,
    delivery_epoch: u64,
}

impl ChangefeedWorker {
    pub(super) async fn process_outbox_record(
        &self,
        fields: &DecodedRow,
        source_domain: &str,
        wal_end: Lsn,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = fields.text("event_id").unwrap_or_default().to_string();
        // [COMMENT]: Mọi durable runtime outbox đều định tuyến bằng UUID Zone
        // typed trực tiếp; JO không còn diễn giải scope string theo từng domain.
        let zone_id = fields.text("zone_id").unwrap_or_default().to_string();
        let job_topic = fields.text("job_topic").unwrap_or_default().to_string();
        let payload_hex = fields.text("payload").unwrap_or_default().to_string();
        let payload_key_id = fields
            .text("payload_key_id")
            .unwrap_or_default()
            .to_string();
        let job_version_str = fields.text("job_version").unwrap_or_default().to_string();
        let resource_id = fields.text("resource_id").unwrap_or_default().to_string();
        let payload_schema_version_str = fields
            .text("payload_schema_version")
            .unwrap_or_default()
            .to_string();
        // Outboxes predating Managed Service do not have this additive field;
        // their initial delivery is epoch zero by definition.
        let delivery_epoch_str = fields.text("delivery_epoch").unwrap_or("0").to_string();
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

        if event_id.is_empty()
            || zone_id.is_empty()
            || job_topic.is_empty()
            || source_domain.is_empty()
            || resource_id.is_empty()
            || payload_key_id.is_empty()
        {
            return Err(Box::new(PermanentChangeError(
                "monitored outbox row is missing event_id/zone route/job_topic".to_string(),
            )));
        }
        if !crate::job_topics::is_command_registered(&source_domain, &job_topic) {
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
        let zone_id_clone = zone_id.clone();
        let payload_hex_clone = payload_hex.clone();
        let job_version_str_clone = job_version_str.clone();
        let resource_id_clone = resource_id.clone();
        let payload_schema_version_str_clone = payload_schema_version_str.clone();
        let delivery_epoch_str_clone = delivery_epoch_str.clone();
        let idle_str_clone = idle_str.clone();
        let source_domain_clone = source_domain.clone();
        let metadata_client = self.metadata_client.clone();
        let wal_lsn = wal_end.to_string();
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
        let publish_result: Result<bool, Box<dyn std::error::Error>> = async move {
            let event_uuid = uuid::Uuid::parse_str(&event_id_clone).map_err(|error| {
                PermanentChangeError(format!("invalid outbox event_id: {error}"))
            })?;
            let target_zone_id = canonical_zone_route(&zone_id_clone).map_err(PermanentChangeError)?;
            let target_zone_uuid = uuid::Uuid::parse_str(&target_zone_id).map_err(|error| {
                PermanentChangeError(format!("invalid target Zone UUID: {error}"))
            })?;
            let job_version = job_version_str_clone
                .parse::<u32>()
                .map_err(|_| PermanentChangeError("invalid job_version".to_string()))?;
            if job_version == 0 {
                return Err(PermanentChangeError("job_version must be positive".to_string()).into());
            }
            let delivery_epoch = delivery_epoch_str_clone
                .parse::<u64>()
                .map_err(|_| PermanentChangeError("invalid delivery_epoch".to_string()))?;
            if delivery_epoch > i64::MAX as u64 {
                return Err(PermanentChangeError(
                    "delivery_epoch exceeds the durable platform range".to_string(),
                )
                .into());
            }

            if source_domain_clone == "MANAGED_SERVICE" {
                // The WAL row is only a notification. Re-read the authoritative
                // row immediately before publication so a stale retry UPDATE
                // cannot enqueue an old protected command after a newer epoch
                // or terminal result has already won the database race.
                let row = metadata_client
                    .query_opt(
                        "SELECT zone_id, job_topic, job_version, delivery_epoch, status,
                                created_at,
                                GREATEST(
                                    pg_wal_lsn_diff(pg_current_wal_lsn(), $2::pg_lsn),
                                    0
                                )::double precision
                         FROM managed_service.managed_service_outbox_records
                         WHERE event_id = $1",
                        &[&event_uuid, &wal_lsn],
                    )
                    .await?;
                let Some(row) = row else {
                    return Err(PermanentChangeError(
                        "managed service outbox row disappeared before publication".to_string(),
                    )
                    .into());
                };
                let current = ManagedServiceAuthoritativeFence {
                    zone_id: row.get(0),
                    job_topic: row.get(1),
                    job_version: row.get(2),
                    delivery_epoch: row.get(3),
                    status: row.get(4),
                };
                let created_at: chrono::DateTime<chrono::Utc> = row.get(5);
                let cdc_lag_bytes: f64 = row.get(6);
                let outbox_age_seconds = chrono::Utc::now()
                    .signed_duration_since(created_at)
                    .num_milliseconds()
                    .max(0) as f64
                    / 1_000.0;
                crate::observability::metrics::MetricsManager::record_managed_service_dispatch_lag(
                    outbox_age_seconds,
                    cdc_lag_bytes,
                );
                let wal = ManagedServiceWalFence {
                    zone_id: target_zone_uuid,
                    job_topic: &job_topic_clone,
                    job_version: i32::try_from(job_version).unwrap_or(i32::MAX),
                    delivery_epoch: i64::try_from(delivery_epoch).unwrap_or(i64::MAX),
                };
                match evaluate_managed_service_fence(&current, &wal)? {
                    ManagedServiceFenceDecision::Publish => {}
                    ManagedServiceFenceDecision::Stale => {
                        Logger::job_log_with_fields(
                            &event_id_clone,
                            &job_topic_clone,
                            0,
                            "changefeed.stale_wal",
                            "MANAGED_SERVICE_OUTBOX_STALE",
                            "Skipped stale managed-service WAL after authoritative fence check",
                            LogFields {
                                event_id: Some(&event_id_clone),
                                source_domain: Some("MANAGED_SERVICE"),
                                job_version: Some(u64::from(job_version)),
                                outcome: Some("stale"),
                                ..LogFields::default()
                            },
                        );
                        crate::observability::metrics::MetricsManager::record_managed_service_outbox_stale();
                        return Ok(false);
                    }
                }
            }

            let payload_bytes = decode_pg_bytea(&payload_hex_clone).map_err(|error| {
                PermanentChangeError(format!("invalid outbox bytea payload: {error}"))
            })?;
            if payload_bytes.is_empty() || payload_bytes.len() > 1_000_256 {
                return Err(PermanentChangeError(
                    "outbox protected payload size is outside the platform limit".to_string(),
                )
                .into());
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
            let expected_key_id = uuid::Uuid::parse_str(&payload_key_id).map_err(|error| {
                PermanentChangeError(format!("invalid outbox payload_key_id: {error}"))
            })?;
            validate_protected_payload(&payload_bytes, target_zone_uuid, expected_key_id)?;
            let topic = self.kafka.zone_command_topic(&target_zone_id);
            {
                use opentelemetry::trace::TraceContextExt;
                opentelemetry::Context::current().span().set_attribute(
                    opentelemetry::KeyValue::new("aurora.zone.id", target_zone_id.clone()),
                );
            }

            let command = build_outbox_command(DurableOutboxCommand {
                event_id: event_uuid,
                job_version,
                job_topic: job_topic_clone.clone(),
                source_domain: source_domain_clone.clone(),
                resource_id: resource_id_clone.clone(),
                payload_schema_version,
                protected_payload: payload_bytes,
                trace_id: trace_id_bytes,
                idle_seconds: idle,
                target_zone_id,
                traceparent: propagation.traceparent,
                tracestate: propagation.tracestate,
                delivery_epoch,
            });

            match self
                .kafka
                // Aggregate ordering is resource-scoped. Replays retain the
                // same key even when a later operation receives a new event_id.
                .publish_message(&topic, resource_id_clone.as_bytes(), &command)
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
                    Ok(true)
                }
                Err(error) => {
                    Err(std::io::Error::other(error).into())
                }
            }
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
        let published = publish_result?;
        if !published {
            return Ok(());
        }

        if source_domain == "MANAGED_SERVICE" {
            let writer = self.managed_service_outbox_writer.as_ref().ok_or_else(|| {
                std::io::Error::other(
                    "managed service outbox writer is not configured for an enabled source",
                )
            })?;
            self.mark_managed_service_outbox_processing(
                writer,
                &event_id,
                &zone_id,
                &job_topic,
                &job_version_str,
                &delivery_epoch_str,
            )
            .await?;
        }

        Ok(())
    }

    pub(super) async fn mark_managed_service_outbox_processing(
        &self,
        writer: &tokio_postgres::Client,
        event_id: &str,
        zone_id: &str,
        job_topic: &str,
        job_version: &str,
        delivery_epoch: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let event_id = uuid::Uuid::parse_str(event_id)
            .map_err(|error| PermanentChangeError(format!("invalid event_id: {error}")))?;
        let zone_id = uuid::Uuid::parse_str(zone_id)
            .map_err(|error| PermanentChangeError(format!("invalid zone_id: {error}")))?;
        let job_version = job_version
            .parse::<i32>()
            .map_err(|_| PermanentChangeError("invalid job_version".to_string()))?;
        let delivery_epoch = delivery_epoch
            .parse::<i64>()
            .map_err(|_| PermanentChangeError("invalid delivery_epoch".to_string()))?;
        let row = writer
            .query_opt(
                "UPDATE managed_service.managed_service_outbox_records
                 SET status = CASE WHEN status = 'PENDING' THEN 'PROCESSING' ELSE status END,
                     updated_at = CASE WHEN status = 'PENDING' THEN NOW() ELSE updated_at END
                 WHERE event_id = $1
                   AND zone_id = $2
                   AND job_topic = $3
                   AND job_version = $4
                   AND delivery_epoch = $5
                   AND status IN ('PENDING', 'PROCESSING')
                   RETURNING status",
                &[
                    &event_id,
                    &zone_id,
                    &job_topic,
                    &job_version,
                    &delivery_epoch,
                ],
            )
            .await?;
        let Some(row) = row else {
            // No row means the WAL record was stale/deleted or its fence was
            // tampered with. Classify it as permanent so the existing changefeed
            // boundary durably quarantines the fingerprint before advancing LSN.
            return Err(PermanentChangeError(
                "managed service outbox processing fence did not match".to_string(),
            )
            .into());
        };
        let status: String = row.get(0);
        Logger::job_log_with_fields(
            &event_id.to_string(),
            job_topic,
            0,
            "changefeed.outbox_processing",
            "MANAGED_SERVICE_OUTBOX_PROCESSING",
            "Managed Service outbox is marked PROCESSING after Kafka durable ACK",
            LogFields {
                event_id: Some(&event_id.to_string()),
                source_domain: Some("MANAGED_SERVICE"),
                job_version: Some(job_version as u64),
                outcome: Some(status.as_str()),
                ..LogFields::default()
            },
        );
        crate::observability::metrics::MetricsManager::record_managed_service_outbox_processing();
        Ok(())
    }

    pub(super) async fn observe_managed_service_backlog(&self) {
        if self.managed_service_outbox_writer.is_none() {
            return;
        }
        let result = self
            .metadata_client
            .query_one(
                "SELECT COUNT(*)::bigint,
                        COALESCE(
                            EXTRACT(EPOCH FROM (NOW() - MIN(created_at))),
                            0
                        )::double precision
                 FROM managed_service.managed_service_outbox_records
                 WHERE status = 'PENDING'",
                &[],
            )
            .await;
        match result {
            Ok(row) => {
                let pending_records: i64 = row.get(0);
                let oldest_age_seconds: f64 = row.get(1);
                crate::observability::metrics::MetricsManager::record_managed_service_backlog(
                    u64::try_from(pending_records).unwrap_or_default(),
                    oldest_age_seconds,
                );
            }
            Err(error) => Logger::sys_warn(
                "changefeed.metrics",
                "Managed Service backlog sample failed; dispatch remains independent from observability",
                &error.to_string(),
            ),
        }
    }
}

fn evaluate_managed_service_fence(
    current: &ManagedServiceAuthoritativeFence,
    wal: &ManagedServiceWalFence<'_>,
) -> Result<ManagedServiceFenceDecision, PermanentChangeError> {
    if current.delivery_epoch > wal.delivery_epoch
        || (current.delivery_epoch == wal.delivery_epoch
            && matches!(current.status.as_str(), "SUCCEEDED" | "FAILED"))
    {
        return Ok(ManagedServiceFenceDecision::Stale);
    }
    if current.zone_id != wal.zone_id
        || current.job_topic != wal.job_topic
        || current.job_version != wal.job_version
        || current.delivery_epoch != wal.delivery_epoch
    {
        return Err(PermanentChangeError(
            "managed service outbox authoritative fence does not match WAL".to_string(),
        ));
    }
    if !matches!(current.status.as_str(), "PENDING" | "PROCESSING") {
        return Err(PermanentChangeError(
            "managed service outbox has an invalid pre-publication status".to_string(),
        ));
    }
    Ok(ManagedServiceFenceDecision::Publish)
}

fn validate_protected_payload(
    payload: &[u8],
    target_zone_id: uuid::Uuid,
    expected_key_id: uuid::Uuid,
) -> Result<(), PermanentChangeError> {
    let protected = ProtectedPayloadV1::decode(payload).map_err(|_| {
        PermanentChangeError("outbox payload is not ProtectedPayloadV1".to_string())
    })?;
    if protected.schema_version != 1
        || protected.encoding
            != PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32
        || protected.encapsulated_key.len() != 32
        || protected.plaintext_size == 0
        || protected.plaintext_size > 1_000_000
    {
        return Err(PermanentChangeError(
            "outbox protected payload contract is invalid".to_string(),
        ));
    }
    if protected.recipient_zone_id.as_slice() != target_zone_id.as_bytes() {
        return Err(PermanentChangeError(
            "outbox protected payload target Zone does not match its durable route".to_string(),
        ));
    }
    if protected.key_id.as_slice() != expected_key_id.as_bytes() {
        return Err(PermanentChangeError(
            "outbox protected payload_key_id does not match protected payload".to_string(),
        ));
    }
    if protected.ciphertext.len() != protected.plaintext_size as usize + 16 {
        return Err(PermanentChangeError(
            "outbox protected payload ciphertext length is invalid".to_string(),
        ));
    }
    Ok(())
}

fn build_outbox_command(input: DurableOutboxCommand) -> JobCommandV1 {
    JobCommandV1 {
        job_id: input.event_id.as_bytes().to_vec(),
        job_version: input.job_version,
        attempt: 0,
        job_topic: input.job_topic,
        source_domain: input.source_domain,
        resource_id: input.resource_id,
        payload_schema_version: input.payload_schema_version,
        // ProtectedPayloadV1 is forwarded exactly as committed. JO never
        // decode/re-encodes this byte string when constructing the outer job.
        payload: input.protected_payload,
        trace_id: input.trace_id,
        idle_seconds: input.idle_seconds,
        reconcile_generation: None,
        target_zone_id: input.target_zone_id,
        transport_schema_version: 1,
        traceparent: input.traceparent,
        tracestate: input.tracestate,
        payload_encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
        delivery_epoch: input.delivery_epoch,
    }
}

#[cfg(test)]
mod tests {
    use super::super::quarantine::changefeed_error_code;
    use super::{
        build_outbox_command, canonical_zone_route, evaluate_managed_service_fence,
        validate_protected_payload, DurableOutboxCommand, ManagedServiceAuthoritativeFence,
        ManagedServiceFenceDecision, ManagedServiceWalFence,
    };
    use crate::infra::kafka::transport_proto::{PayloadEncodingV1, ProtectedPayloadV1};
    use prost::Message;

    const ZONE_ID: &str = "019f3d3e-997d-7894-9236-c5122634cb4f";

    #[test]
    fn canonical_zone_route_accepts_typed_zone_id() {
        assert_eq!(canonical_zone_route(ZONE_ID).unwrap(), ZONE_ID);
    }

    #[test]
    fn canonical_zone_route_rejects_scope_strings_and_nil_zone() {
        assert!(canonical_zone_route(&format!("zone:{ZONE_ID}")).is_err());
        assert!(canonical_zone_route("platform").is_err());
        assert!(canonical_zone_route("global").is_err());
        assert!(canonical_zone_route("00000000-0000-0000-0000-000000000000").is_err());
    }

    #[test]
    fn changefeed_dlq_taxonomy_is_bounded_and_route_specific() {
        assert_eq!(
            changefeed_error_code("invalid target Zone UUID"),
            "COMMAND_ZONE_MISMATCH"
        );
        assert_eq!(
            changefeed_error_code("outbox protected payload metadata mismatch"),
            "COMMAND_HASH_MISMATCH"
        );
        assert_eq!(
            changefeed_error_code("invalid payload_schema_version"),
            "COMMAND_CONTRACT_INVALID"
        );
    }

    #[test]
    fn authoritative_fence_allows_replay_but_rejects_stale_or_cross_zone_wal() {
        let zone_id = uuid::Uuid::parse_str(ZONE_ID).unwrap();
        let wal = ManagedServiceWalFence {
            zone_id,
            job_topic: "managed_service.instance.execute",
            job_version: 1,
            delivery_epoch: 2,
        };
        let processing = ManagedServiceAuthoritativeFence {
            zone_id,
            job_topic: wal.job_topic.to_string(),
            job_version: 1,
            delivery_epoch: 2,
            status: "PROCESSING".to_string(),
        };
        assert_eq!(
            evaluate_managed_service_fence(&processing, &wal).unwrap(),
            ManagedServiceFenceDecision::Publish
        );

        let newer = ManagedServiceAuthoritativeFence {
            delivery_epoch: 3,
            ..processing
        };
        assert_eq!(
            evaluate_managed_service_fence(&newer, &wal).unwrap(),
            ManagedServiceFenceDecision::Stale
        );

        let cross_zone = ManagedServiceAuthoritativeFence {
            zone_id: uuid::Uuid::new_v4(),
            delivery_epoch: 2,
            status: "PENDING".to_string(),
            ..newer
        };
        assert!(evaluate_managed_service_fence(&cross_zone, &wal).is_err());
    }

    #[test]
    fn managed_service_replay_keeps_exact_protected_bytes_and_stable_outer_encoding() {
        let zone_id = uuid::Uuid::parse_str(ZONE_ID).unwrap();
        let key_id = uuid::Uuid::from_u128(1);
        let protected = ProtectedPayloadV1 {
            schema_version: 1,
            recipient_zone_id: zone_id.as_bytes().to_vec(),
            key_id: key_id.as_bytes().to_vec(),
            encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
            encapsulated_key: vec![7; 32],
            ciphertext: vec![9; 48],
            plaintext_size: 32,
        }
        .encode_to_vec();
        validate_protected_payload(&protected, zone_id, key_id).unwrap();

        let event_id = uuid::Uuid::new_v4();
        let resource_id = uuid::Uuid::new_v5(&event_id, b"instance").to_string();
        let build = || {
            build_outbox_command(DurableOutboxCommand {
                event_id,
                job_version: 1,
                job_topic: "managed_service.instance.execute".to_string(),
                source_domain: "MANAGED_SERVICE".to_string(),
                resource_id: resource_id.clone(),
                payload_schema_version: 1,
                protected_payload: protected.clone(),
                trace_id: Vec::new(),
                idle_seconds: None,
                target_zone_id: zone_id.to_string(),
                traceparent: String::new(),
                tracestate: String::new(),
                delivery_epoch: 3,
            })
        };
        let first = build();
        let replay = build();
        assert_eq!(first.payload, protected);
        assert_eq!(first.encode_to_vec(), replay.encode_to_vec());
        assert_eq!(first.delivery_epoch, 3);
    }

    #[test]
    fn protected_payload_fence_distinguishes_zone_and_key_mismatch() {
        let zone_id = uuid::Uuid::parse_str(ZONE_ID).unwrap();
        let key_id = uuid::Uuid::from_u128(1);
        let protected = ProtectedPayloadV1 {
            schema_version: 1,
            recipient_zone_id: zone_id.as_bytes().to_vec(),
            key_id: key_id.as_bytes().to_vec(),
            encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
            encapsulated_key: vec![7; 32],
            ciphertext: vec![9; 17],
            plaintext_size: 1,
        }
        .encode_to_vec();
        let zone_error = validate_protected_payload(&protected, uuid::Uuid::new_v4(), key_id)
            .unwrap_err()
            .to_string();
        assert_eq!(changefeed_error_code(&zone_error), "COMMAND_ZONE_MISMATCH");
        let key_error = validate_protected_payload(&protected, zone_id, uuid::Uuid::new_v4())
            .unwrap_err()
            .to_string();
        assert_eq!(changefeed_error_code(&key_error), "COMMAND_HASH_MISMATCH");
    }
}
