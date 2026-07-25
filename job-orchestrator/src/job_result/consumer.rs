use super::l1_dispatcher;
use crate::config::Config;
use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::{LogFields, Logger};
use prost::Message;
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::NoTls;

const INVALID_RESULT_ERROR_CODE: &str = "JOB_RESULT_PROTO_INVALID";

/// [COMMENT]: Kết quả Kafka chỉ commit sau khi L2 transaction DB và realtime notification hoàn tất.
pub struct JobResultConsumer {
    config: Config,
    kafka: Arc<KafkaTransport>,
    shared_redis: redis::Client,
}

impl JobResultConsumer {
    pub fn new(config: Config, kafka: Arc<KafkaTransport>, shared_redis: redis::Client) -> Self {
        Self {
            config,
            kafka,
            shared_redis,
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        let mut notification_redis = self.shared_redis.get_connection_manager().await?;
        let (mut client, connection) =
            tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(error) = connection.await {
                Logger::sys_error(
                    "job_result.postgres",
                    "JobResultConsumer PostgreSQL connection failed",
                    &error.to_string(),
                );
            }
        });

        let topic = self.kafka.result_topic();
        let consumer = self
            .kafka
            .consumer("aurora-job-orchestrator-results-v1", &topic)
            .await
            .map_err(std::io::Error::other)?;
        loop {
            let records = consumer.poll(Duration::from_secs(1)).await?;
            for record in records {
                let payload = record.value.unwrap_or_default();
                let decoded =
                    l1_dispatcher::job_proto::JobExecutionResultProto::decode(payload.as_ref());
                let valid_result = decoded.as_ref().is_ok_and(|result| {
                    result.job_id.len() == 16
                        && !result.job_topic.trim().is_empty()
                        && matches!(result.source_domain.as_str(), "MAIL" | "STORAGE")
                        && matches!(
                            result.result_status.as_str(),
                            "PROCESSING" | "SUCCEEDED" | "FAILED"
                        )
                        && crate::observability::otel::OtelTracer::is_valid_propagation_context(
                            &result.traceparent,
                            &result.tracestate,
                        )
                });
                if !valid_result {
                    crate::observability::metrics::MetricsManager::record_result_rejected();
                    // [COMMENT]: Poison result phải được ghi DLQ bằng acks=all trước khi commit offset gốc.
                    let dlq_event_id = result_dlq_event_id(
                        &record.topic,
                        record.partition,
                        record.offset,
                        INVALID_RESULT_ERROR_CODE,
                    );
                    let dlq = DeadLetterRecordV1 {
                        event_id: dlq_event_id.as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: INVALID_RESULT_ERROR_CODE.to_string(),
                        error_message: "JobExecutionResultProto failed strict validation"
                            .to_string(),
                        original_payload: payload.to_vec(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let dlq_event_id = dlq_event_id.to_string();
                    let key = dlq.event_id.clone();
                    if let Err(error) = self
                        .kafka
                        .publish_message(&self.kafka.dead_letter_topic(), &key, &dlq)
                        .await
                    {
                        Logger::sys_error_with_fields(
                            "job_result.dlq",
                            "JOB_RESULT_DLQ_PUBLISH_FAILED",
                            "Invalid Kafka result could not be durably published to DLQ; source offset remains unsettled",
                            &error,
                            LogFields {
                                event_id: Some(&dlq_event_id),
                                kafka_topic: Some(&record.topic),
                                kafka_partition: Some(record.partition),
                                kafka_offset: Some(record.offset),
                                retryable: Some(true),
                                outcome: Some("failed"),
                                ..LogFields::default()
                            },
                        );
                        return Err(std::io::Error::other(error).into());
                    }
                    crate::observability::metrics::MetricsManager::record_dlq_published();
                    Logger::sys_warn_with_fields(
                        "job_result.validation",
                        INVALID_RESULT_ERROR_CODE,
                        "Invalid Kafka result was durably published to DLQ before source settlement",
                        "",
                        LogFields {
                            event_id: Some(&dlq_event_id),
                            kafka_topic: Some(&record.topic),
                            kafka_partition: Some(record.partition),
                            kafka_offset: Some(record.offset),
                            outcome: Some("quarantined"),
                            ..LogFields::default()
                        },
                    );
                    let settle_context = crate::observability::otel::OtelTracer::start_current_span(
                        format!("commit {}", record.topic),
                        opentelemetry::trace::SpanKind::Client,
                        vec![
                            opentelemetry::KeyValue::new("messaging.system", "kafka"),
                            opentelemetry::KeyValue::new("messaging.operation.type", "settle"),
                            opentelemetry::KeyValue::new(
                                "messaging.destination.name",
                                record.topic.clone(),
                            ),
                        ],
                    );
                    let settle_result = self
                        .kafka
                        .commit(
                            &consumer,
                            &record.topic,
                            record.partition,
                            record.offset + 1,
                        )
                        .with_context(settle_context.clone())
                        .await;
                    crate::observability::otel::OtelTracer::finish_span(
                        &settle_context,
                        settle_result
                            .as_ref()
                            .err()
                            .map(|_| "KAFKA_RESULT_SETTLEMENT_FAILED"),
                    );
                    settle_result.map_err(std::io::Error::other)?;
                    continue;
                };
                let result = decoded.expect("validated result must be decoded");
                crate::observability::metrics::MetricsManager::inc_results_received();
                let parent_context = crate::observability::otel::OtelTracer::extract_context(
                    &result.traceparent,
                    &result.tracestate,
                );
                let job_id = uuid::Uuid::from_slice(&result.job_id)
                    .map(|value| value.to_string())
                    .unwrap_or_default();
                let result_job_topic = result.job_topic.clone();
                let result_source_domain = result.source_domain.clone();
                let result_job_version = u64::from(result.job_version);
                let process_context =
                    crate::observability::otel::OtelTracer::start_span_with_parent(
                        format!("process {}", result.job_topic),
                        opentelemetry::trace::SpanKind::Consumer,
                        vec![
                            opentelemetry::KeyValue::new("messaging.system", "kafka"),
                            opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                            opentelemetry::KeyValue::new(
                                "messaging.destination.name",
                                record.topic.clone(),
                            ),
                            opentelemetry::KeyValue::new(
                                "messaging.kafka.partition",
                                i64::from(record.partition),
                            ),
                            opentelemetry::KeyValue::new("messaging.kafka.offset", record.offset),
                            opentelemetry::KeyValue::new("aurora.job.id", job_id.clone()),
                            opentelemetry::KeyValue::new(
                                "aurora.job.topic",
                                result_job_topic.clone(),
                            ),
                            opentelemetry::KeyValue::new(
                                "aurora.job.outcome",
                                result.result_status,
                            ),
                        ],
                        &parent_context,
                    );

                use opentelemetry::trace::FutureExt;
                let process_result: Result<(), Box<dyn std::error::Error>> = async {
                    l1_dispatcher::dispatch_result(
                        payload.as_ref(),
                        &mut client,
                        &mut notification_redis,
                    )
                    .await?;
                    // [COMMENT]: Consumer span covers the actual durability boundary, not
                    // only the PostgreSQL update. A successful span means source offset committed.
                    let settle_context = crate::observability::otel::OtelTracer::start_current_span(
                        format!("commit {}", record.topic),
                        opentelemetry::trace::SpanKind::Client,
                        vec![
                            opentelemetry::KeyValue::new("messaging.system", "kafka"),
                            opentelemetry::KeyValue::new("messaging.operation.type", "settle"),
                            opentelemetry::KeyValue::new(
                                "messaging.destination.name",
                                record.topic.clone(),
                            ),
                            opentelemetry::KeyValue::new(
                                "messaging.kafka.partition",
                                i64::from(record.partition),
                            ),
                            opentelemetry::KeyValue::new("messaging.kafka.offset", record.offset),
                        ],
                    );
                    let settle_result = self
                        .kafka
                        .commit(
                            &consumer,
                            &record.topic,
                            record.partition,
                            record.offset + 1,
                        )
                        .with_context(settle_context.clone())
                        .await;
                    crate::observability::otel::OtelTracer::finish_span(
                        &settle_context,
                        settle_result
                            .as_ref()
                            .err()
                            .map(|_| "KAFKA_RESULT_SETTLEMENT_FAILED"),
                    );
                    settle_result.map_err(std::io::Error::other)?;
                    crate::observability::metrics::MetricsManager::record_result_settled();
                    Ok(())
                }
                .with_context(process_context.clone())
                .await;
                crate::observability::otel::OtelTracer::finish_span(
                    &process_context,
                    process_result
                        .as_ref()
                        .err()
                        .map(|_| "JOB_RESULT_PROCESS_FAILED"),
                );

                if let Err(error) = process_result {
                    crate::observability::metrics::MetricsManager::record_result_failed();
                    Logger::sys_error_with_fields(
                        "job_result.process",
                        "JOB_RESULT_PROCESS_FAILED",
                        "Kafka result xử lý thất bại; offset chưa commit",
                        &error.to_string(),
                        LogFields {
                            event_id: Some(&job_id),
                            operation_id: Some(&job_id),
                            source_domain: Some(&result_source_domain),
                            job_version: Some(result_job_version),
                            kafka_topic: Some(&record.topic),
                            kafka_partition: Some(record.partition),
                            kafka_offset: Some(record.offset),
                            retryable: Some(true),
                            outcome: Some("failed"),
                            ..LogFields::default()
                        },
                    );
                    // [COMMENT]: Dừng consumer trước khi record offset cao hơn được commit;
                    // Kubernetes restart sẽ replay từ offset durable cuối cùng.
                    return Err(error);
                }
            }
        }
    }
}

fn result_dlq_event_id(
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
) -> uuid::Uuid {
    // The same poison record may replay after DLQ ACK but before source commit. A deterministic
    // event ID lets downstream diagnostics correlate those at-least-once DLQ publications.
    let identity = format!("{source_topic}:{source_partition}:{source_offset}:{error_code}");
    uuid::Uuid::new_v5(&uuid::Uuid::NAMESPACE_URL, identity.as_bytes())
}

#[cfg(test)]
mod tests {
    use super::result_dlq_event_id;

    #[test]
    fn poison_result_dlq_identity_is_stable_per_source_record() {
        let first = result_dlq_event_id("aurora.jobs.results.v1", 2, 41, "INVALID");
        let replay = result_dlq_event_id("aurora.jobs.results.v1", 2, 41, "INVALID");
        let next_offset = result_dlq_event_id("aurora.jobs.results.v1", 2, 42, "INVALID");

        assert_eq!(first, replay);
        assert_ne!(first, next_offset);
    }
}
