use std::sync::Arc;
use std::time::Duration;

use opentelemetry::trace::FutureExt;
use sha2::{Digest, Sha256};
use tokio::sync::mpsc;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::{KafkaDelivery, KafkaTransport};
use crate::job_runtime::model::{QueuedJob, ValidatedJob};
use crate::observability::logger::{LogFields, Logger};

const MAX_CONCURRENT_RETRY_PUBLISHES: usize = 32;
const MAX_RESULT_MESSAGE_BYTES: usize = 4_096;
const MAX_DLQ_ERROR_MESSAGE_BYTES: usize = 1_024;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CompletionStatus {
    Processing,
    Succeeded,
    Failed,
    Retryable,
}

impl CompletionStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Processing => "PROCESSING",
            Self::Succeeded => "SUCCEEDED",
            Self::Failed => "FAILED",
            Self::Retryable => "RETRYABLE",
        }
    }

    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Succeeded | Self::Failed)
    }
}

#[derive(Clone, Debug)]
pub struct JobExecutionResult {
    pub job_id: String,
    pub job_version: u32,
    pub attempt: u32,
    pub status: CompletionStatus,
    pub error_code: Option<String>,
    pub message: String,
    pub job_topic: String,
    pub source_domain: String,
    pub trace_id: String,
    pub result_payload: Vec<u8>,
    pub result_payload_schema_version: u32,
}

impl JobExecutionResult {
    pub fn processing(job: &ValidatedJob) -> Self {
        Self::new(
            job,
            CompletionStatus::Processing,
            None,
            String::new(),
            Vec::new(),
            0,
        )
    }

    pub fn timeout(job: &ValidatedJob) -> Self {
        Self::new(
            job,
            CompletionStatus::Failed,
            Some("EXECUTION_TIMEOUT".to_string()),
            "Job execution cancelled by the lease watchdog after its deadline".to_string(),
            Vec::new(),
            0,
        )
    }

    pub fn from_executor(
        job: &ValidatedJob,
        outcome: Result<ExecutionResult, ExecutorError>,
    ) -> Self {
        match outcome {
            Ok(result) => Self::new(
                job,
                CompletionStatus::Succeeded,
                None,
                result.message,
                result.result_payload,
                result.result_payload_schema_version,
            ),
            Err(ExecutorError::ExecutionFailed(message)) => Self::new(
                job,
                CompletionStatus::Failed,
                Some("EXECUTION_FAILED".to_string()),
                message,
                Vec::new(),
                0,
            ),
            Err(ExecutorError::Retryable(message)) => Self::new(
                job,
                CompletionStatus::Retryable,
                Some("TRANSIENT_INFRASTRUCTURE".to_string()),
                message,
                Vec::new(),
                0,
            ),
        }
    }

    pub fn mark_retry_exhausted(&mut self) {
        self.status = CompletionStatus::Failed;
        self.error_code = Some("RETRY_EXHAUSTED".to_string());
    }

    fn new(
        job: &ValidatedJob,
        status: CompletionStatus,
        error_code: Option<String>,
        message: String,
        result_payload: Vec<u8>,
        result_payload_schema_version: u32,
    ) -> Self {
        Self {
            job_id: job.job_id.clone(),
            job_version: job.job_version,
            attempt: job.attempt,
            status,
            error_code,
            message: crate::observability::logger::sanitize_for_durable_event(
                &message,
                MAX_RESULT_MESSAGE_BYTES,
            ),
            job_topic: job.job_topic.clone(),
            source_domain: job.source_domain.clone(),
            trace_id: job.trace_id.clone(),
            result_payload,
            result_payload_schema_version,
        }
    }
}

#[derive(Clone, Copy, Debug, Default)]
pub struct CompletionOutcome {
    pub result_durable: bool,
    pub source_settled: bool,
}

pub struct CompletionRequest {
    pub job: Arc<ValidatedJob>,
    pub delivery: KafkaDelivery,
    pub result: JobExecutionResult,
}

pub struct RetryRequest {
    job: Arc<ValidatedJob>,
    next_attempt: u32,
    reason: &'static str,
    topic: String,
    traceparent: String,
    tracestate: String,
    delivery: KafkaDelivery,
    delay: Duration,
}

pub fn build_retry_request(
    queued: &QueuedJob,
    next_attempt: u32,
    delay: Duration,
    reason: &'static str,
) -> RetryRequest {
    let current =
        crate::observability::otel::OtelTracer::inject_context(&opentelemetry::Context::current());
    let (traceparent, tracestate) = if current.traceparent.is_empty() {
        (
            queued.job.traceparent.clone(),
            queued.job.tracestate.clone(),
        )
    } else {
        (current.traceparent, current.tracestate)
    };
    RetryRequest {
        job: queued.job.clone(),
        next_attempt,
        reason,
        topic: queued.delivery.topic.clone(),
        traceparent,
        tracestate,
        delivery: queued.delivery.clone(),
        delay,
    }
}

pub async fn enqueue_retry(
    retry_tx: &mpsc::Sender<RetryRequest>,
    request: RetryRequest,
) -> Result<(), String> {
    // A bounded await applies backpressure without sleeping inside the worker
    // for the whole retry delay. If shutdown closes the receiver, the original
    // Kafka source remains unsettled and is replayed by the next assignment.
    retry_tx
        .send(request)
        .await
        .map_err(|error| format!("retry scheduler unavailable: {error}"))
}

pub async fn run_retry_scheduler(
    mut retries: mpsc::Receiver<RetryRequest>,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    let mut tasks = JoinSet::new();
    let mut receiver_open = true;
    let zone_id = crate::config::Config::get_global().zone_id.clone();

    loop {
        crate::observability::metrics::WorkerControlMetrics::record_job_retry_queue_depth(
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
                        "job.completion.retry",
                        &format!("Retry publisher task failed: {error}"),
                        "JOB_RETRY_TASK_FAILED",
                    );
                    // The JoinError no longer carries the owned source
                    // delivery. Exit the critical scheduler so the process
                    // restarts and Kafka replays the unsettled command.
                    return;
                }
            }
            request = retries.recv(), if receiver_open && tasks.len() < MAX_CONCURRENT_RETRY_PUBLISHES => {
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
    retry: RetryRequest,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    tokio::select! {
        biased;
        _ = shutdown.cancelled() => return,
        _ = tokio::time::sleep(retry.delay) => {}
    }

    let parent = crate::observability::otel::OtelTracer::extract_context(
        &retry.traceparent,
        &retry.tracestate,
    );
    let publish_context = crate::observability::otel::OtelTracer::start_span_with_parent(
        format!("send {}", retry.topic),
        opentelemetry::trace::SpanKind::Producer,
        vec![
            opentelemetry::KeyValue::new("messaging.system", "kafka"),
            opentelemetry::KeyValue::new("messaging.operation.type", "send"),
            opentelemetry::KeyValue::new("messaging.destination.name", retry.topic.clone()),
            opentelemetry::KeyValue::new("aurora.job.id", retry.job.job_id.clone()),
            opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(retry.next_attempt)),
            opentelemetry::KeyValue::new("aurora.job.retry_reason", retry.reason),
        ],
        &parent,
    );
    // The delayed producer span, not the completed executor span, is the direct
    // parent of the next Kafka consumer attempt.
    let propagation = crate::observability::otel::OtelTracer::inject_context(&publish_context);
    // Materialize the protobuf only when the delay expires. Pending retries
    // retain one Arc payload instead of cloning up to 1 MiB per queued item.
    let command = retry.job.command_for_attempt(
        retry.next_attempt,
        propagation.traceparent,
        propagation.tracestate,
    );
    // Retry must stay on the same Kafka partition as the first attempt so
    // per-resource ordering is preserved across the whole execution lifecycle.
    let key = retry.job.resource_id.as_bytes().to_vec();
    let publish_result = tokio::select! {
        biased;
        _ = shutdown.cancelled() => return,
        result = kafka
            .publish_message(&retry.topic, &key, &command)
            .with_context(publish_context.clone()) => result,
    };
    crate::observability::otel::OtelTracer::finish_span(
        &publish_context,
        publish_result
            .as_ref()
            .err()
            .map(|_| "KAFKA_RETRY_PUBLISH_FAILED"),
    );

    match publish_result {
        Ok(()) => {
            crate::observability::metrics::WorkerControlMetrics::record_job_runtime_event(
                &crate::config::Config::get_global().zone_id,
                "retry_published",
            );
            match settle_delivery(&retry.delivery).await {
                Ok(_) => {}
                Err(error) => {
                    Logger::sys_error(
                        "job.completion.retry",
                        &format!(
                            "Retry for job {} is durable but source settlement failed: {error}",
                            retry.job.job_id
                        ),
                        "JOB_RETRY_SETTLEMENT_FAILED",
                    );
                }
            }
        }
        Err(error) => {
            crate::observability::metrics::WorkerControlMetrics::record_job_runtime_event(
                &crate::config::Config::get_global().zone_id,
                "retry_publish_failed",
            );
            Logger::sys_error(
                "job.completion.retry",
                &format!(
                    "Could not durably publish retry for job {}; source remains unsettled: {error}",
                    retry.job.job_id
                ),
                "JOB_RETRY_PUBLISH_FAILED",
            );
        }
    }
}

pub async fn publish_result(
    kafka: &KafkaTransport,
    result: &JobExecutionResult,
) -> Result<(), String> {
    let job_id_bytes = uuid::Uuid::parse_str(&result.job_id)
        .map(|value| value.as_bytes().to_vec())
        .map_err(|error| format!("result job_id is not a UUID: {error}"))?;
    let trace_id_bytes = decode_hex(&result.trace_id)?;
    let result_topic = kafka.result_topic();
    let producer_context = crate::observability::otel::OtelTracer::start_current_span(
        format!("send {result_topic}"),
        opentelemetry::trace::SpanKind::Producer,
        vec![
            opentelemetry::KeyValue::new("messaging.system", "kafka"),
            opentelemetry::KeyValue::new("messaging.operation.type", "send"),
            opentelemetry::KeyValue::new("messaging.destination.name", result_topic.clone()),
            opentelemetry::KeyValue::new("aurora.job.id", result.job_id.clone()),
            opentelemetry::KeyValue::new("aurora.job.version", i64::from(result.job_version)),
            opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(result.attempt)),
            opentelemetry::KeyValue::new("aurora.job.outcome", result.status.as_str()),
        ],
    );
    let propagation = crate::observability::otel::OtelTracer::inject_context(&producer_context);
    let proto = job_proto::JobExecutionResultProto {
        job_id: job_id_bytes,
        job_version: result.job_version,
        attempt: result.attempt,
        result_status: result.status.as_str().to_string(),
        job_topic: result.job_topic.clone(),
        trace_id: trace_id_bytes,
        error_code: result.error_code.clone(),
        message: result.message.clone(),
        source_domain: result.source_domain.clone(),
        traceparent: propagation.traceparent,
        tracestate: propagation.tracestate,
        result_payload: result.result_payload.clone(),
        result_payload_schema_version: result.result_payload_schema_version,
    };
    let key = proto.job_id.clone();
    let publish_result = kafka
        .publish_message(&result_topic, &key, &proto)
        .with_context(producer_context.clone())
        .await;
    crate::observability::otel::OtelTracer::finish_span(
        &producer_context,
        publish_result
            .as_ref()
            .err()
            .map(|_| "KAFKA_RESULT_PUBLISH_FAILED"),
    );
    publish_result
}

pub async fn complete_terminal(
    kafka: &KafkaTransport,
    job: &ValidatedJob,
    delivery: &KafkaDelivery,
    result: &JobExecutionResult,
) -> CompletionOutcome {
    if !result.status.is_terminal() {
        return CompletionOutcome::default();
    }

    if job.reconcile_generation.is_some() {
        if result.status == CompletionStatus::Succeeded {
            return settle_after_durable(delivery).await;
        }
        return publish_dead_letter_and_settle(
            kafka,
            delivery,
            result
                .error_code
                .as_deref()
                .unwrap_or("RECONCILE_EXECUTION_FAILED"),
            &result.message,
            &job.payload,
            "job.completion.reconcile",
        )
        .await;
    }

    match publish_result(kafka, result).await {
        Ok(()) => settle_after_durable(delivery).await,
        Err(error) => {
            Logger::sys_error(
                "job.completion.terminal",
                &format!(
                    "Terminal result for job {} is not durable; source remains unsettled: {error}",
                    job.job_id
                ),
                "JOB_TERMINAL_RESULT_NOT_DURABLE",
            );
            CompletionOutcome::default()
        }
    }
}

pub async fn quarantine_invalid_command(
    kafka: &KafkaTransport,
    delivery: &KafkaDelivery,
    error_code: &'static str,
    error_message: &str,
    raw_payload: &[u8],
) -> CompletionOutcome {
    publish_dead_letter_and_settle(
        kafka,
        delivery,
        error_code,
        error_message,
        raw_payload,
        "job.intake.quarantine",
    )
    .await
}

async fn publish_dead_letter_and_settle(
    kafka: &KafkaTransport,
    delivery: &KafkaDelivery,
    error_code: &str,
    error_message: &str,
    original_payload: &[u8],
    log_target: &'static str,
) -> CompletionOutcome {
    let event_id = stable_dlq_event_id(
        &delivery.topic,
        delivery.partition,
        delivery.offset,
        error_code,
    );
    let dead_letter = build_dead_letter_record(
        event_id.clone(),
        delivery.topic.clone(),
        delivery.partition,
        delivery.offset,
        error_code,
        error_message,
        original_payload,
    );
    let event_id_text = uuid::Uuid::from_slice(&event_id)
        .map(|value| value.to_string())
        .unwrap_or_default();
    let fields = || LogFields {
        event_id: Some(event_id_text.as_str()),
        kafka_topic: Some(delivery.topic.as_str()),
        kafka_partition: Some(delivery.partition),
        kafka_offset: Some(delivery.offset),
        assignment_epoch: Some(delivery.assignment_epoch),
        outcome: Some("quarantined"),
        ..LogFields::default()
    };

    match kafka
        .publish_message(&kafka.dead_letter_topic(), &event_id, &dead_letter)
        .await
    {
        Ok(()) => match settle_delivery(delivery).await {
            Ok(true) => {
                Logger::sys_warn_with_fields(
                    log_target,
                    error_code,
                    "Kafka job was durably quarantined before source settlement",
                    "",
                    fields(),
                );
                CompletionOutcome {
                    result_durable: true,
                    source_settled: true,
                }
            }
            Ok(false) => {
                Logger::sys_warn_with_fields(
                    log_target,
                    "KAFKA_SETTLEMENT_WAITING_FOR_LOWER_OFFSET",
                    "Kafka job was durably quarantined and is waiting for a lower offset to become terminal",
                    "",
                    LogFields {
                        outcome: Some("waiting_for_lower_offset"),
                        ..fields()
                    },
                );
                CompletionOutcome {
                    result_durable: true,
                    source_settled: false,
                }
            }
            Err(error) => {
                Logger::sys_error_with_fields(
                    log_target,
                    "JOB_DLQ_SOURCE_SETTLEMENT_FAILED",
                    "DLQ publish succeeded but source settlement failed; replay is expected",
                    &error,
                    LogFields {
                        retryable: Some(true),
                        outcome: Some("replay_expected"),
                        ..fields()
                    },
                );
                CompletionOutcome {
                    result_durable: true,
                    source_settled: false,
                }
            }
        },
        Err(error) => {
            Logger::sys_error_with_fields(
                log_target,
                "JOB_DLQ_PUBLISH_FAILED",
                "Kafka job remains unsettled because durable DLQ publish failed",
                &error,
                LogFields {
                    retryable: Some(true),
                    outcome: Some("unsettled"),
                    ..fields()
                },
            );
            CompletionOutcome::default()
        }
    }
}

fn build_dead_letter_record(
    event_id: Vec<u8>,
    source_topic: String,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
    error_message: &str,
    original_payload: &[u8],
) -> DeadLetterRecordV1 {
    let payload_digest = Sha256::digest(original_payload);
    let diagnostic = format!(
        "{} [original_payload_omitted=true original_payload_bytes={} original_payload_sha256={payload_digest:x}]",
        crate::observability::logger::sanitize_for_durable_event(error_message, 512),
        original_payload.len(),
    );
    DeadLetterRecordV1 {
        event_id,
        source_topic,
        source_partition,
        source_offset,
        error_code: error_code.to_string(),
        error_message: crate::observability::logger::sanitize_for_durable_event(
            &diagnostic,
            MAX_DLQ_ERROR_MESSAGE_BYTES,
        ),
        // Invalid commands have not crossed a schema/secret boundary. A digest
        // is sufficient for correlation without turning DLQ into secret storage.
        original_payload: Vec::new(),
        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        schema_version: 1,
    }
}

async fn settle_after_durable(delivery: &KafkaDelivery) -> CompletionOutcome {
    match settle_delivery(delivery).await {
        Ok(source_settled) => CompletionOutcome {
            result_durable: true,
            source_settled,
        },
        Err(error) => {
            Logger::sys_warn(
                "job.completion.settlement",
                &format!(
                    "Durable completion for {}:{}:{} could not settle source: {error}",
                    delivery.topic, delivery.partition, delivery.offset
                ),
                "KAFKA_OFFSET_COMMIT_DEFERRED",
            );
            CompletionOutcome {
                result_durable: true,
                source_settled: false,
            }
        }
    }
}

async fn settle_delivery(delivery: &KafkaDelivery) -> Result<bool, String> {
    crate::observability::otel::OtelTracer::trace_result(
        format!("commit {}", delivery.topic),
        opentelemetry::trace::SpanKind::Client,
        vec![
            opentelemetry::KeyValue::new("messaging.system", "kafka"),
            opentelemetry::KeyValue::new("messaging.operation.type", "settle"),
            opentelemetry::KeyValue::new("messaging.destination.name", delivery.topic.clone()),
            opentelemetry::KeyValue::new(
                "messaging.kafka.partition",
                i64::from(delivery.partition),
            ),
            opentelemetry::KeyValue::new("messaging.kafka.offset", delivery.offset),
        ],
        delivery.settle(),
    )
    .await
}

pub async fn run_completion_reporter(
    mut reports: mpsc::Receiver<CompletionRequest>,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    let mut shutdown_requested = false;
    loop {
        if shutdown_requested {
            match reports.recv().await {
                Some(report) => {
                    complete_terminal(&kafka, &report.job, &report.delivery, &report.result).await;
                }
                None => return,
            }
            continue;
        }
        tokio::select! {
            report = reports.recv() => {
                let Some(report) = report else {
                    return;
                };
                complete_terminal(&kafka, &report.job, &report.delivery, &report.result).await;
            }
            _ = shutdown.cancelled() => shutdown_requested = true,
        }
    }
}

fn stable_dlq_event_id(
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
) -> Vec<u8> {
    // Replaying publish-before-settle keeps the same logical DLQ identity.
    uuid::Uuid::new_v5(
        &uuid::Uuid::NAMESPACE_OID,
        format!("{source_topic}\0{source_partition}\0{source_offset}\0{error_code}").as_bytes(),
    )
    .as_bytes()
    .to_vec()
}

fn decode_hex(value: &str) -> Result<Vec<u8>, String> {
    if value.is_empty() {
        return Ok(Vec::new());
    }
    if !value.len().is_multiple_of(2) {
        return Err("trace_id hex length must be even".to_string());
    }
    (0..value.len())
        .step_by(2)
        .map(|index| {
            u8::from_str_radix(&value[index..index + 2], 16)
                .map_err(|error| format!("trace_id contains invalid hex: {error}"))
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stable_dlq_identity_is_replay_safe() {
        assert_eq!(
            stable_dlq_event_id("topic", 1, 2, "INVALID"),
            stable_dlq_event_id("topic", 1, 2, "INVALID")
        );
        assert_ne!(
            stable_dlq_event_id("topic", 1, 2, "INVALID"),
            stable_dlq_event_id("topic", 1, 3, "INVALID")
        );
    }

    #[test]
    fn trace_decoder_fails_closed() {
        assert_eq!(decode_hex("0011ff").unwrap(), vec![0x00, 0x11, 0xff]);
        assert!(decode_hex("00zz").is_err());
        assert!(decode_hex("0").is_err());
    }

    #[test]
    fn dlq_omits_untrusted_raw_payload_and_redacts_diagnostic() {
        let record = build_dead_letter_record(
            uuid::Uuid::nil().as_bytes().to_vec(),
            "commands".to_string(),
            1,
            2,
            "INVALID",
            "upstream password=do-not-publish",
            b"plaintext-customer-secret",
        );
        assert!(record.original_payload.is_empty());
        assert!(!record.error_message.contains("do-not-publish"));
        assert!(!record.error_message.contains("plaintext-customer-secret"));
        assert!(record.error_message.contains("original_payload_sha256="));
    }
}
