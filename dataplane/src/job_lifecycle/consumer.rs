use prost::Message;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::transport_proto::{DeadLetterRecordV1, JobCommandV1};
use crate::infra::kafka::{KafkaDelivery, KafkaSettlement, KafkaTransport};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_lifecycle::admission::AdmissionController;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::{LogFields, Logger};

pub struct JobConsumer;

impl JobConsumer {
    /// [COMMENT]: Kafka intake dùng manual commit; terminal offset chỉ được settle ở JobRunner sau durable result/retry/DLQ.
    pub async fn start_zone_ingestion(
        config: Arc<crate::config::Config>,
        kafka: Arc<KafkaTransport>,
        zone_kv: Arc<ZoneKvStore>,
        tx: async_channel::Sender<JobPayload>,
        cancel_token: CancellationToken,
        admitted_jobs: Arc<AtomicUsize>,
    ) {
        let topic = kafka.zone_command_topic(&config.zone_id);
        let group_name = format!("aurora-dataplane-zone-{}-v1", config.zone_id);
        let (consumer, rebalance_fence) = match kafka.consumer(group_name.clone(), &topic, 32).await
        {
            Ok(value) => value,
            Err(error) => {
                Logger::sys_error(
                    "job.ingestion",
                    &format!("Kafka consumer bootstrap failed for {topic}: {error}"),
                    "KAFKA_CONSUMER_BOOTSTRAP_FAILED",
                );
                return;
            }
        };
        let settlement = KafkaSettlement::new(consumer.clone(), rebalance_fence.clone());
        let mut admission_controller = AdmissionController::new();
        let mut last_logged_status = String::new();

        Logger::sys_info(
            "job.ingestion",
            &format!("Kafka ingestion started topic={topic} group={group_name}"),
        );

        loop {
            let zone_status = match zone_kv.read_zone_metadata().await {
                Ok(metadata) => metadata.status,
                Err(error) => {
                    if last_logged_status != "kv_unavailable" {
                        Logger::sys_error(
                            "job.ingestion",
                            "Không thể đọc Zone metadata; ingestion tạm dừng theo fail-closed",
                            &error,
                        );
                    }
                    "kv_unavailable".to_string()
                }
            };
            if zone_status != "active" {
                last_logged_status = zone_status;
                tokio::select! {
                    _ = cancel_token.cancelled() => break,
                    _ = sleep(Duration::from_secs(1)) => {}
                }
                continue;
            }
            last_logged_status.clear();

            let admission = admission_controller
                .evaluate(admitted_jobs.load(Ordering::SeqCst), config.max_workers);
            if admission.is_broken {
                tokio::select! {
                    _ = cancel_token.cancelled() => break,
                    _ = sleep(Duration::from_millis(500)) => {}
                }
                continue;
            }
            if admission.pacing_delay_ms > 0 {
                tokio::select! {
                    _ = cancel_token.cancelled() => break,
                    _ = sleep(Duration::from_millis(admission.pacing_delay_ms)) => {}
                }
            }

            let records = tokio::select! {
                _ = cancel_token.cancelled() => break,
                result = consumer.poll(Duration::from_millis(500)) => {
                    match result {
                        Ok(records) => records,
                        Err(error) => {
                            Logger::sys_error(
                                "job.ingestion",
                                &format!("Kafka poll failed for {topic}: {error}"),
                                "KAFKA_POLL_FAILED",
                            );
                            sleep(Duration::from_secs(1)).await;
                            continue;
                        }
                    }
                }
            };
            // [COMMENT]: Lag lấy ngay từ consumer fetch state, không quét broker bằng một polling client phụ.
            kafka.observe_job_lag(&consumer).await;
            let assignment_epoch = rebalance_fence.epoch();

            for record in records {
                settlement
                    .register(
                        assignment_epoch,
                        &record.topic,
                        record.partition,
                        record.offset,
                    )
                    .await;
                let delivery = KafkaDelivery::new(
                    record.topic.clone(),
                    record.partition,
                    record.offset,
                    assignment_epoch,
                    settlement.clone(),
                );
                let raw_payload = record.value.clone().unwrap_or_default();

                let command = match JobCommandV1::decode(raw_payload.as_ref()) {
                    Ok(command)
                        if command.transport_schema_version == 1
                            && command.job_id.len() == 16
                            && command.payload_schema_version > 0
                            && !command.job_topic.trim().is_empty()
                            && !command.source_domain.trim().is_empty()
                            && !command.resource_id.trim().is_empty()
                            && crate::observability::otel::OtelTracer::is_valid_propagation_context(
                                &command.traceparent,
                                &command.tracestate,
                            ) =>
                    {
                        command
                    }
                    _ => {
                        // [COMMENT]: Poison record phải đi DLQ bền vững trước khi bỏ qua offset gốc.
                        let error_code = "JOB_COMMAND_PROTO_INVALID";
                        let dlq = DeadLetterRecordV1 {
                            event_id: stable_job_dlq_event_id(
                                &record.topic,
                                record.partition,
                                record.offset,
                                error_code,
                            ),
                            source_topic: record.topic.clone(),
                            source_partition: record.partition,
                            source_offset: record.offset,
                            error_code: error_code.to_string(),
                            error_message: "JobCommandV1 failed strict validation".to_string(),
                            original_payload: raw_payload.to_vec(),
                            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                            schema_version: 1,
                        };
                        quarantine_job_record(kafka.as_ref(), &delivery, dlq, assignment_epoch)
                            .await;
                        continue;
                    }
                };

                // [COMMENT]: Dataplane chỉ có một command namespace theo Zone.
                // Target rỗng hoặc khác Zone đều là vi phạm trust boundary và phải vào DLQ.
                if command.target_zone_id != config.zone_id {
                    let error_code = "JOB_TARGET_ZONE_MISMATCH";
                    let dlq = DeadLetterRecordV1 {
                        event_id: stable_job_dlq_event_id(
                            &record.topic,
                            record.partition,
                            record.offset,
                            error_code,
                        ),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: error_code.to_string(),
                        error_message: format!(
                            "envelope target {:?} does not match consumer zone {}",
                            command.target_zone_id, config.zone_id
                        ),
                        original_payload: raw_payload.to_vec(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    quarantine_job_record(kafka.as_ref(), &delivery, dlq, assignment_epoch).await;
                    continue;
                }

                let job_id = match uuid::Uuid::from_slice(&command.job_id) {
                    Ok(value) => value.to_string(),
                    Err(error) => {
                        Logger::sys_error_with_fields(
                            "job.ingestion.validation",
                            "JOB_ID_UUID_INVALID",
                            "Validated job command contained an invalid job UUID",
                            &error.to_string(),
                            LogFields {
                                kafka_topic: Some(&record.topic),
                                kafka_partition: Some(record.partition),
                                kafka_offset: Some(record.offset),
                                assignment_epoch: Some(assignment_epoch),
                                outcome: Some("rejected"),
                                ..LogFields::default()
                            },
                        );
                        continue;
                    }
                };
                let trace_id = command
                    .trace_id
                    .iter()
                    .map(|byte| format!("{byte:02x}"))
                    .collect::<String>();
                let payload = JobPayload {
                    job_id,
                    job_version: command.job_version,
                    attempt: command.attempt,
                    job_topic: command.job_topic,
                    source_domain: command.source_domain,
                    resource_id: command.resource_id,
                    payload_schema_version: command.payload_schema_version,
                    payload: Arc::from(command.payload),
                    trace_id,
                    traceparent: command.traceparent,
                    tracestate: command.tracestate,
                    idle: command.idle_seconds,
                    reconcile_generation: command.reconcile_generation,
                    target_zone_id: command.target_zone_id,
                    kafka_delivery: Some(delivery.clone()),
                    zone_lease: None,
                };

                if matches!(
                    payload.job_topic.as_str(),
                    "mail.consumer.upsert"
                        | "mail.consumer.delete"
                        | "mail.template.version_published"
                        | "mail.template.deleted"
                ) {
                    let mut hasher = std::collections::hash_map::DefaultHasher::new();
                    std::env::var("HOSTNAME")
                        .unwrap_or_else(|_| std::process::id().to_string())
                        .hash(&mut hasher);
                    payload.job_id.hash(&mut hasher);
                    sleep(Duration::from_millis(hasher.finish() % 250)).await;
                }

                admitted_jobs.fetch_add(1, Ordering::SeqCst);
                let send_result = tokio::select! {
                    _ = cancel_token.cancelled() => Err("shutdown"),
                    result = tx.send(payload) => result.map_err(|_| "worker channel closed"),
                };
                if let Err(error) = send_result {
                    admitted_jobs.fetch_sub(1, Ordering::SeqCst);
                    Logger::sys_error(
                        "job.ingestion",
                        &format!("Kafka job dispatch failed: {error}"),
                        "CHANNEL_DISPATCH_ERROR",
                    );
                }
            }
        }
    }

    /// [COMMENT]: Định tuyến nghiệp vụ không nhận transport realtime; mail watch/report chạy
    /// độc lập qua NATS Core supervisor để job executor không thể truy cập Central soft state.
    pub async fn dispatch_workload(
        payload: JobPayload,
        worker_pool: Arc<crate::workerpool::pool::WorkerLifecycleManager>,
        zone_id: &str,
    ) -> Result<crate::executor::ExecutionResult, crate::executor::ExecutorError> {
        let topic = payload.job_topic.clone();
        let Some(first_dot) = topic.find('.') else {
            return Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                "Invalid job topic format: {topic}"
            )));
        };
        let (workload, rest) = topic.split_at(first_dot);
        let action = &rest[1..];
        match workload {
            "mail" => {
                crate::executor::mail::dispatch_mail_job(action, payload, worker_pool, zone_id)
                    .await
            }
            "vps" => crate::executor::hypervisor::dispatch_vps_job(action, payload).await,
            "storage" => crate::executor::storage::dispatch_storage_job(action, payload).await,
            _ => Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                "Unsupported workload type: {workload}"
            ))),
        }
    }
}

fn stable_job_dlq_event_id(
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
) -> Vec<u8> {
    // [COMMENT]: Reprocessing the same poison offset after publish-before-settle must retain the
    // same logical DLQ identity; consumers can then deduplicate at-least-once publication.
    uuid::Uuid::new_v5(
        &uuid::Uuid::NAMESPACE_OID,
        format!("{source_topic}\0{source_partition}\0{source_offset}\0{error_code}").as_bytes(),
    )
    .as_bytes()
    .to_vec()
}

async fn quarantine_job_record(
    kafka: &KafkaTransport,
    delivery: &KafkaDelivery,
    dlq: DeadLetterRecordV1,
    assignment_epoch: u64,
) {
    let event_id = uuid::Uuid::from_slice(&dlq.event_id)
        .map(|value| value.to_string())
        .unwrap_or_default();
    let fields = || LogFields {
        event_id: Some(event_id.as_str()),
        kafka_topic: Some(dlq.source_topic.as_str()),
        kafka_partition: Some(dlq.source_partition),
        kafka_offset: Some(dlq.source_offset),
        assignment_epoch: Some(assignment_epoch),
        outcome: Some("quarantined"),
        ..LogFields::default()
    };
    let event_key = dlq.event_id.clone();
    match kafka
        .publish_message(&kafka.dead_letter_topic(), &event_key, &dlq)
        .await
    {
        Ok(()) => match delivery.settle().await {
            Ok(()) => Logger::sys_warn_with_fields(
                "job.ingestion.quarantine",
                &dlq.error_code,
                "Invalid Kafka job command was durably quarantined before source settlement",
                "",
                fields(),
            ),
            Err(error) => Logger::sys_error_with_fields(
                "job.ingestion.quarantine",
                "JOB_DLQ_SOURCE_SETTLEMENT_FAILED",
                "DLQ publish succeeded but source Kafka offset settlement failed; replay is expected",
                &error,
                LogFields {
                    retryable: Some(true),
                    outcome: Some("replay_expected"),
                    ..fields()
                },
            ),
        },
        Err(error) => Logger::sys_error_with_fields(
            "job.ingestion.quarantine",
            "JOB_DLQ_PUBLISH_FAILED",
            "Invalid Kafka job command remains unsettled because durable DLQ publish failed",
            &error,
            LogFields {
                retryable: Some(true),
                outcome: Some("unsettled"),
                ..fields()
            },
        ),
    }
}
