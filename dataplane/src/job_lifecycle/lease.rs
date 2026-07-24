//! Execution-boundary lease acquisition and bounded retry publishing.

use std::sync::Arc;
use std::time::Duration;

use sha2::{Digest, Sha256};
use tokio::sync::mpsc;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::transport_proto::JobCommandV1;
use crate::infra::kafka::{KafkaDelivery, KafkaTransport};
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;

pub const JOB_EXECUTION_LEASE_TTL_SECS: u64 = 30;
const MAX_CONCURRENT_LEASE_RETRY_PUBLISHES: usize = 32;

pub struct JobExecutionLeaseRetry {
    job_id: String,
    topic: String,
    key: Vec<u8>,
    command: JobCommandV1,
    delivery: KafkaDelivery,
    delay: Duration,
}

pub async fn acquire_job_execution_lease(
    zone_kv: &ZoneKvStore,
    job_id: &str,
) -> Result<Option<ZoneLease>, String> {
    let job_key_digest = Sha256::digest(job_id.as_bytes());
    let lock_key = format!("lease.job.{job_key_digest:x}");
    let owner_id = format!(
        "{}-{}",
        std::env::var("HOSTNAME").unwrap_or_else(|_| std::process::id().to_string()),
        uuid::Uuid::new_v4()
    );
    zone_kv
        .acquire_lease(
            &lock_key,
            &owner_id,
            Duration::from_secs(JOB_EXECUTION_LEASE_TTL_SECS),
        )
        .await
}

pub fn build_job_execution_lease_retry(
    payload: &JobPayload,
    delay: Duration,
) -> Result<JobExecutionLeaseRetry, &'static str> {
    let delivery = payload
        .kafka_delivery
        .clone()
        .ok_or("JOB_LEASE_RETRY_KAFKA_DELIVERY_REQUIRED")?;
    let job_id =
        uuid::Uuid::parse_str(&payload.job_id).map_err(|_| "JOB_LEASE_RETRY_JOB_ID_INVALID")?;
    let key = job_id.as_bytes().to_vec();
    let command = JobCommandV1 {
        job_id: key.clone(),
        job_version: payload.job_version,
        attempt: payload.attempt,
        job_topic: payload.job_topic.clone(),
        source_domain: payload.source_domain.clone(),
        resource_id: payload.resource_id.clone(),
        payload_schema_version: payload.payload_schema_version,
        payload: payload.payload.to_vec(),
        trace_id: decode_trace_id(&payload.trace_id),
        idle_seconds: payload.idle,
        reconcile_generation: payload.reconcile_generation,
        target_zone_id: payload.target_zone_id.clone(),
        transport_schema_version: 1,
        traceparent: payload.traceparent.clone(),
        tracestate: payload.tracestate.clone(),
    };
    Ok(JobExecutionLeaseRetry {
        job_id: payload.job_id.clone(),
        topic: delivery.topic.clone(),
        key,
        command,
        delivery,
        delay,
    })
}

pub async fn run_job_execution_lease_retry_publisher(
    mut retries: mpsc::Receiver<JobExecutionLeaseRetry>,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    let mut tasks = JoinSet::new();
    let mut receiver_open = true;
    let zone_id = crate::config::Config::get_global().zone_id.clone();

    loop {
        crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_retry_queue_depth(
            &zone_id,
            retries.len(),
        );
        if shutdown.is_cancelled() {
            retries.close();
            tasks.abort_all();
            while tasks.join_next().await.is_some() {}
            return;
        }
        if !receiver_open && tasks.is_empty() {
            return;
        }

        tokio::select! {
            biased;
            _ = shutdown.cancelled() => {}
            result = tasks.join_next(), if !tasks.is_empty() => {
                if let Some(Err(error)) = result {
                    Logger::sys_error(
                        "job.execution_lease_retry",
                        &format!("Lease retry publisher task failed: {error}"),
                        "JOB_LEASE_RETRY_TASK_FAILED",
                    );
                }
            }
            request = retries.recv(), if receiver_open && tasks.len() < MAX_CONCURRENT_LEASE_RETRY_PUBLISHES => {
                match request {
                    Some(request) => {
                        let kafka = kafka.clone();
                        let task_shutdown = shutdown.clone();
                        tasks.spawn(async move {
                            publish_retry_after_delay(request, kafka, task_shutdown).await;
                        });
                    }
                    None => receiver_open = false,
                }
            }
        }
    }
}

async fn publish_retry_after_delay(
    retry: JobExecutionLeaseRetry,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    tokio::select! {
        biased;
        _ = shutdown.cancelled() => return,
        _ = tokio::time::sleep(retry.delay) => {}
    }

    let publish_result = tokio::select! {
        biased;
        _ = shutdown.cancelled() => return,
        result = kafka.publish_message(&retry.topic, &retry.key, &retry.command) => result,
    };
    match publish_result {
        Ok(()) => {
            crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_event(
                &crate::config::Config::get_global().zone_id,
                "retry_published",
            );
            let settlement = tokio::select! {
                biased;
                _ = shutdown.cancelled() => return,
                result = retry.delivery.settle() => result,
            };
            if let Err(error) = settlement {
                crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_event(
                    &crate::config::Config::get_global().zone_id,
                    "retry_settlement_failed",
                );
                Logger::sys_error(
                    "job.execution_lease_retry",
                    &format!(
                        "Lease retry for job {} is durable but source settlement failed: {error}",
                        retry.job_id
                    ),
                    "JOB_LEASE_RETRY_SETTLEMENT_FAILED",
                );
            }
        }
        Err(error) => {
            crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_event(
                &crate::config::Config::get_global().zone_id,
                "retry_publish_failed",
            );
            Logger::sys_error(
                "job.execution_lease_retry",
                &format!(
                    "Could not durably publish lease retry for job {}; source remains unsettled: {error}",
                    retry.job_id
                ),
                "JOB_LEASE_RETRY_PUBLISH_FAILED",
            );
        }
    }
}

fn decode_trace_id(trace_id: &str) -> Vec<u8> {
    (0..trace_id.len())
        .step_by(2)
        .filter_map(|index| trace_id.get(index..index.saturating_add(2)))
        .filter_map(|pair| u8::from_str_radix(pair, 16).ok())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::decode_trace_id;

    #[test]
    fn trace_id_decoder_rejects_invalid_pairs_without_panicking() {
        assert_eq!(decode_trace_id("0011ff"), vec![0x00, 0x11, 0xff]);
        assert_eq!(decode_trace_id("00zz11"), vec![0x00, 0x11]);
        assert_eq!(decode_trace_id("0"), Vec::<u8>::new());
    }
}
