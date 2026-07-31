use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use tokio::time::{sleep, Instant};
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::{KafkaDelivery, KafkaSettlement, KafkaTransport};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::admission::{release_admitted_job, ExecutionCapacity, IntakeAdmission};
use crate::job_runtime::completion::quarantine_invalid_command;
use crate::job_runtime::model::{QueuedJob, ValidatedJob};
use crate::observability::logger::Logger;
use crate::security::jobpayload::PayloadKeyring;

const ZONE_STATUS_REFRESH_INTERVAL: Duration = Duration::from_secs(1);
const ZONE_STATUS_READ_TIMEOUT: Duration = Duration::from_secs(3);
const CONSUMER_BOOTSTRAP_MAX_BACKOFF: Duration = Duration::from_secs(30);
const MAX_UNSETTLED_RECORDS_PER_READY_WORKER: usize = 4;

/// Runs the single Kafka intake lane owned by this Dataplane process.
///
/// Kafka offsets are only registered here. Settlement remains exclusively in
/// completion/retry code after a durable downstream boundary.
pub async fn run_zone_job_intake(
    config: Arc<crate::config::Config>,
    kafka: Arc<KafkaTransport>,
    zone_kv: Arc<ZoneKvStore>,
    tx: async_channel::Sender<QueuedJob>,
    cancel_token: CancellationToken,
    admitted_jobs: Arc<AtomicUsize>,
    execution_capacity: Arc<dyn ExecutionCapacity>,
    payload_keyring: Arc<PayloadKeyring>,
) {
    let topic = kafka.zone_command_topic(&config.zone_id);
    let group_name = format!("aurora-dataplane-zone-{}-v1", config.zone_id);
    // Poll batch is bounded by the initial execution lane, not configured max,
    // so one fetch cannot defeat the admitted-work budget on a mostly idle pod.
    let max_poll_records = config
        .min_workers
        .saturating_mul(2)
        .min(config.job_queue_capacity)
        .clamp(1, 32) as i32;
    let mut bootstrap_backoff = Duration::from_millis(500);

    let (consumer, rebalance_fence) = loop {
        let result = tokio::select! {
            _ = cancel_token.cancelled() => return,
            result = kafka.consumer(group_name.clone(), &topic, max_poll_records) => result,
        };
        match result {
            Ok(value) => break value,
            Err(error) => {
                Logger::sys_error(
                    "job.intake",
                    &format!(
                        "Kafka consumer bootstrap failed for {topic}; retrying in {:?}: {error}",
                        bootstrap_backoff
                    ),
                    "KAFKA_CONSUMER_BOOTSTRAP_FAILED",
                );
                let jitter = Duration::from_millis(rand::random::<u64>() % 250);
                tokio::select! {
                    _ = cancel_token.cancelled() => return,
                    _ = sleep(bootstrap_backoff.saturating_add(jitter)) => {}
                }
                bootstrap_backoff = bootstrap_backoff
                    .saturating_mul(2)
                    .min(CONSUMER_BOOTSTRAP_MAX_BACKOFF);
            }
        }
    };

    let settlement = KafkaSettlement::new(consumer.clone(), rebalance_fence.clone());
    let mut admission = IntakeAdmission::new();
    let mut zone_status = "unknown".to_string();
    let mut next_zone_refresh = Instant::now();
    let mut last_logged_zone_status = String::new();
    let mut settlement_backpressured = false;

    Logger::sys_info(
        "job.intake",
        &format!("Kafka Zone intake started topic={topic} group={group_name}"),
    );

    loop {
        if Instant::now() >= next_zone_refresh {
            let metadata_result = tokio::select! {
                _ = cancel_token.cancelled() => break,
                result = tokio::time::timeout(
                    ZONE_STATUS_READ_TIMEOUT,
                    zone_kv.read_zone_metadata(),
                ) => result,
            };
            zone_status = match metadata_result {
                Ok(Ok(metadata)) => metadata.status,
                Ok(Err(error)) => {
                    if last_logged_zone_status != "kv_unavailable" {
                        Logger::sys_error(
                            "job.intake.zone_gate",
                            "Zone metadata is unavailable; intake is fail-closed",
                            &error,
                        );
                    }
                    "kv_unavailable".to_string()
                }
                Err(_) => {
                    if last_logged_zone_status != "kv_unavailable" {
                        Logger::sys_error(
                            "job.intake.zone_gate",
                            "Zone metadata read timed out; intake is fail-closed",
                            "ZONE_METADATA_READ_TIMEOUT",
                        );
                    }
                    "kv_unavailable".to_string()
                }
            };
            next_zone_refresh = Instant::now() + ZONE_STATUS_REFRESH_INTERVAL;
        }

        if zone_status != "active" {
            if last_logged_zone_status != zone_status {
                Logger::sys_warn(
                    "job.intake.zone_gate",
                    &format!("Zone intake paused because status is {zone_status:?}"),
                    "ZONE_NOT_ACTIVE",
                );
                last_logged_zone_status.clone_from(&zone_status);
            }
            tokio::select! {
                _ = cancel_token.cancelled() => break,
                _ = sleep(Duration::from_millis(250)) => {}
            }
            continue;
        }
        if !last_logged_zone_status.is_empty() {
            Logger::sys_info(
                "job.intake.zone_gate",
                "Zone status is active; Kafka intake resumed",
            );
            last_logged_zone_status.clear();
        }

        let ready_workers = execution_capacity.ready_workers();
        let unsettled_records = settlement.pending_records().await;
        let settlement_budget = ready_workers
            .max(1)
            .saturating_mul(MAX_UNSETTLED_RECORDS_PER_READY_WORKER);
        crate::observability::metrics::WorkerControlMetrics::record_kafka_unsettled_records(
            &config.zone_id,
            unsettled_records,
        );
        if unsettled_records >= settlement_budget {
            if !settlement_backpressured {
                settlement_backpressured = true;
                Logger::sys_warn(
                    "job.intake.settlement",
                    &format!(
                        "Kafka intake paused with {unsettled_records}/{settlement_budget} unsettled records"
                    ),
                    "KAFKA_SETTLEMENT_WINDOW_FULL",
                );
            }
            tokio::select! {
                _ = cancel_token.cancelled() => break,
                _ = sleep(Duration::from_millis(100)) => {}
            }
            continue;
        }
        if settlement_backpressured {
            settlement_backpressured = false;
            Logger::sys_info(
                "job.intake.settlement",
                "Kafka settlement window recovered; intake resumed",
            );
        }

        let admitted = admitted_jobs.load(Ordering::Relaxed);
        let admission_result =
            admission.evaluate(admitted, ready_workers, config.job_queue_capacity);
        if admission_result.is_open || admitted >= admission_result.budget {
            tokio::select! {
                _ = cancel_token.cancelled() => break,
                _ = sleep(Duration::from_millis(250)) => {}
            }
            continue;
        }
        if admission_result.pacing_delay_ms > 0 {
            tokio::select! {
                _ = cancel_token.cancelled() => break,
                _ = sleep(Duration::from_millis(admission_result.pacing_delay_ms)) => {}
            }
        }

        let records = tokio::select! {
            _ = cancel_token.cancelled() => break,
            result = consumer.poll(Duration::from_millis(500)) => {
                match result {
                    Ok(records) => records,
                    Err(error) => {
                        Logger::sys_error(
                            "job.intake",
                            &format!("Kafka poll failed for {topic}: {error}"),
                            "KAFKA_POLL_FAILED",
                        );
                        tokio::select! {
                            _ = cancel_token.cancelled() => break,
                            _ = sleep(Duration::from_secs(1)) => {}
                        }
                        continue;
                    }
                }
            }
        };
        // Lag comes from the active consumer fetch state; no second broker scan.
        kafka.observe_job_lag(&consumer).await;
        let assignment_epoch = rebalance_fence.epoch();

        for record in records {
            if let Err(error) = settlement
                .register(
                    assignment_epoch,
                    &record.topic,
                    record.partition,
                    record.offset,
                )
                .await
            {
                // Rebalance ownership changed after poll. Do not execute a
                // record that can no longer be safely settled by this consumer.
                Logger::sys_warn(
                    "job.intake",
                    &format!(
                        "Kafka record {}:{}:{} skipped after assignment fence changed: {error}",
                        record.topic, record.partition, record.offset
                    ),
                    "KAFKA_RECORD_ASSIGNMENT_STALE",
                );
                continue;
            }
            let delivery = KafkaDelivery::new(
                record.topic.clone(),
                record.partition,
                record.offset,
                assignment_epoch,
                settlement.clone(),
            );
            let raw_payload = record.value.clone().unwrap_or_default();
            let job = match ValidatedJob::decode(
                raw_payload.as_ref(),
                &config.zone_id,
                config.kafka_max_job_attempts,
                payload_keyring.as_ref(),
            ) {
                Ok(job) => job,
                Err(error) => {
                    if error.retryable {
                        Logger::sys_error(
                            "job.intake",
                            "Dataplane cannot open a protected command with its loaded keyring; leaving the offset uncommitted and restarting fail-closed",
                            error.code,
                        );
                        return;
                    }
                    quarantine_invalid_command(
                        kafka.as_ref(),
                        &delivery,
                        error.code,
                        &error.message,
                        raw_payload.as_ref(),
                    )
                    .await;
                    continue;
                }
            };

            admitted_jobs.fetch_add(1, Ordering::Relaxed);
            let queued = QueuedJob { job, delivery };
            let send_result = tokio::select! {
                _ = cancel_token.cancelled() => Err("shutdown"),
                result = tx.send(queued) => result.map_err(|_| "worker channel closed"),
            };
            if let Err(error) = send_result {
                release_admitted_job(&admitted_jobs, "intake_dispatch_failure");
                Logger::sys_error(
                    "job.intake",
                    &format!("Bounded job queue rejected a Kafka record: {error}"),
                    "JOB_QUEUE_DISPATCH_FAILED",
                );
                if error == "worker channel closed" {
                    return;
                }
            }
        }
    }

    Logger::sys_info("job.intake", "Kafka Zone intake stopped");
}
