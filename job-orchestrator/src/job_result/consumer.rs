use super::l1_dispatcher;
use crate::config::Config;
use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use prost::Message;
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::NoTls;

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
                    // [COMMENT]: Poison result phải được ghi DLQ bằng acks=all trước khi commit offset gốc.
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: "JOB_RESULT_PROTO_INVALID".to_string(),
                        error_message: "JobExecutionResultProto failed strict validation"
                            .to_string(),
                        original_payload: payload.to_vec(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let key = dlq.event_id.clone();
                    self.kafka
                        .publish_message(&self.kafka.dead_letter_topic(), &key, &dlq)
                        .await
                        .map_err(std::io::Error::other)?;
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
                let parent_context = crate::observability::otel::OtelTracer::extract_context(
                    &result.traceparent,
                    &result.tracestate,
                );
                let job_id = uuid::Uuid::from_slice(&result.job_id)
                    .map(|value| value.to_string())
                    .unwrap_or_default();
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
                            opentelemetry::KeyValue::new("aurora.job.id", job_id),
                            opentelemetry::KeyValue::new("aurora.job.topic", result.job_topic),
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
                    Logger::sys_error(
                        "job_result.process",
                        "Kafka result xử lý thất bại; offset chưa commit",
                        &error.to_string(),
                    );
                    // [COMMENT]: Dừng consumer trước khi record offset cao hơn được commit;
                    // Kubernetes restart sẽ replay từ offset durable cuối cùng.
                    return Err(error);
                }
            }
        }
    }
}
