use prost::Message;
use sha2::{Digest, Sha256};
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::transport_proto::{DeadLetterRecordV1, JobCommandV1};
use crate::infra::kafka::{KafkaDelivery, KafkaSettlement, KafkaTransport};
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_lifecycle::admission::AdmissionController;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;

pub struct JobConsumer;

impl JobConsumer {
    /// [COMMENT]: Kafka intake dùng manual commit; terminal offset chỉ được settle ở JobRunner sau durable result/retry/DLQ.
    #[allow(clippy::too_many_arguments)]
    pub async fn start_ingestion(
        config: Arc<crate::config::Config>,
        kafka: Arc<KafkaTransport>,
        zone_kv: Arc<ZoneKvStore>,
        tx: tokio::sync::mpsc::Sender<JobPayload>,
        cancel_token: CancellationToken,
        active_jobs: Arc<AtomicUsize>,
        topic: String,
        group_name: String,
    ) {
        let (consumer, rebalance_fence) = match kafka
            .consumer(group_name.clone(), &topic, 32)
            .await
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
                .evaluate(active_jobs.load(Ordering::SeqCst), config.max_workers);
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
            if topic == kafka.zone_command_topic(&config.zone_id) {
                // [COMMENT]: Lag lấy ngay từ consumer fetch state, không quét broker bằng một polling client phụ.
                kafka.observe_job_lag(&consumer).await;
            }
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
                            && !command.resource_id.trim().is_empty() =>
                    {
                        command
                    }
                    _ => {
                        // [COMMENT]: Poison record phải đi DLQ bền vững trước khi bỏ qua offset gốc.
                        let dlq = DeadLetterRecordV1 {
                            event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                            source_topic: record.topic.clone(),
                            source_partition: record.partition,
                            source_offset: record.offset,
                            error_code: "JOB_COMMAND_PROTO_INVALID".to_string(),
                            error_message: "JobCommandV1 failed strict validation".to_string(),
                            original_payload: raw_payload.to_vec(),
                            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                            schema_version: 1,
                        };
                        let event_key = dlq.event_id.clone();
                        if kafka
                            .publish_message(&kafka.dead_letter_topic(), &event_key, &dlq)
                            .await
                            .is_ok()
                        {
                            let _ = delivery.settle().await;
                        }
                        continue;
                    }
                };

                // [COMMENT]: Topic Zone và target_zone_id phải đồng thuận; sai routing fail-close vào DLQ.
                if !command.target_zone_id.is_empty()
                    && command.target_zone_id != config.zone_id
                    && record.topic == kafka.zone_command_topic(&config.zone_id)
                {
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: "JOB_TARGET_ZONE_MISMATCH".to_string(),
                        error_message: format!(
                            "envelope target {} does not match consumer zone {}",
                            command.target_zone_id, config.zone_id
                        ),
                        original_payload: raw_payload.to_vec(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let event_key = dlq.event_id.clone();
                    if kafka
                        .publish_message(&kafka.dead_letter_topic(), &event_key, &dlq)
                        .await
                        .is_ok()
                    {
                        let _ = delivery.settle().await;
                    }
                    continue;
                }

                let job_id = match uuid::Uuid::from_slice(&command.job_id) {
                    Ok(value) => value.to_string(),
                    Err(_) => continue,
                };
                let trace_id = command
                    .trace_id
                    .iter()
                    .map(|byte| format!("{byte:02x}"))
                    .collect::<String>();
                let mut payload = JobPayload {
                    job_id,
                    job_version: command.job_version,
                    attempt: command.attempt,
                    job_topic: command.job_topic,
                    source_domain: command.source_domain,
                    resource_id: command.resource_id,
                    payload_schema_version: command.payload_schema_version,
                    payload: command.payload,
                    trace_id,
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

                let job_key_digest = Sha256::digest(payload.job_id.as_bytes());
                let lock_key = format!("lease.job.{job_key_digest:x}");
                let owner_id = format!(
                    "{}-{}",
                    std::env::var("HOSTNAME").unwrap_or_else(|_| std::process::id().to_string()),
                    uuid::Uuid::new_v4()
                );
                match zone_kv
                    .acquire_lease(&lock_key, &owner_id, Duration::from_secs(30))
                    .await
                {
                    Ok(Some(lease)) => payload.zone_lease = Some(lease),
                    Ok(None) => {
                        // [COMMENT]: Chờ xấp xỉ lease TTL rồi mới tạo một durable retry; republish mỗi 250ms
                        // khi owner đang chạy sẽ khuếch đại duplicate thành broker storm.
                        let retry_kafka = kafka.clone();
                        let retry_topic = record.topic.clone();
                        let retry_key = command.job_id.clone();
                        let retry_payload = raw_payload.clone();
                        let retry_delivery = delivery.clone();
                        tokio::spawn(async move {
                            let jitter = rand::random::<u64>() % 2_000;
                            sleep(Duration::from_millis(30_000 + jitter)).await;
                            if retry_kafka
                                .publish(&retry_topic, &retry_key, retry_payload.as_ref())
                                .await
                                .is_ok()
                            {
                                // [COMMENT]: Rebalance epoch trong delivery ngăn task trì hoãn commit assignment mới.
                                let _ = retry_delivery.settle().await;
                            }
                        });
                        continue;
                    }
                    Err(error) => {
                        Logger::sys_error(
                            "job.ingestion",
                            &format!("Zone KV lease acquisition failed: {error}"),
                            "ZONE_KV_LEASE_ERROR",
                        );
                        continue;
                    }
                }

                active_jobs.fetch_add(1, Ordering::SeqCst);
                let lease = payload.zone_lease.clone();
                let send_result = tokio::select! {
                    _ = cancel_token.cancelled() => Err("shutdown"),
                    result = tx.send(payload) => result.map_err(|_| "worker channel closed"),
                };
                if let Err(error) = send_result {
                    active_jobs.fetch_sub(1, Ordering::SeqCst);
                    if let Some(lease) = lease {
                        let _ = zone_kv.release_lease(&lease).await;
                    }
                    Logger::sys_error(
                        "job.ingestion",
                        &format!("Kafka job dispatch failed: {error}"),
                        "CHANNEL_DISPATCH_ERROR",
                    );
                }
            }
        }
    }

    /// Định tuyến nghiệp vụ; Redis tham số ở đây chỉ dành cho mail runtime watch/report ngắn hạn.
    pub async fn dispatch_workload(
        payload: JobPayload,
        worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
        runtime_redis: Arc<RedisClientManager>,
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
                crate::executor::mail::dispatch_mail_job(
                    action,
                    payload,
                    worker_pool,
                    runtime_redis,
                    zone_id,
                )
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
