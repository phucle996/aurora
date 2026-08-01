use super::{apply, contract, quarantine};
use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::{LogFields, Logger};
use crate::outbox::SharedStreamPublisher;
use opentelemetry::trace::FutureExt;
use std::sync::Arc;
use std::time::Duration;

pub struct ResultWorker {
    config: Config,
    kafka: Arc<KafkaTransport>,
    shared_redis: redis::Client,
    ownership_publisher: Arc<SharedStreamPublisher>,
}

impl ResultWorker {
    pub fn new(
        config: Config,
        kafka: Arc<KafkaTransport>,
        shared_redis: redis::Client,
        ownership_publisher: Arc<SharedStreamPublisher>,
    ) -> Self {
        Self {
            config,
            kafka,
            shared_redis,
            ownership_publisher,
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        let mut notification_redis =
            crate::infra::redis::manager(&self.shared_redis, &self.config.shared_redis).await?;
        let mut result_postgres = self.config.postgres.clone();
        result_postgres.database_url = self.config.postgres.result_database_url.clone();
        let mut client =
            crate::infra::postgres::connect(&result_postgres, "results.postgres").await?;
        let transaction_read_only: String = client
            .query_one("SHOW transaction_read_only", &[])
            .await?
            .get(0);
        if transaction_read_only.eq_ignore_ascii_case("on") {
            return Err("job result settlement PostgreSQL role is read-only".into());
        }

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
                let result = match contract::decode(payload.as_ref()) {
                    Ok(result) => result,
                    Err(contract_error) => {
                        crate::observability::metrics::MetricsManager::record_result_rejected();
                        let event_id = quarantine::publish(
                            &self.kafka,
                            &record.topic,
                            record.partition,
                            record.offset,
                            contract_error.code,
                            payload.as_ref(),
                        )
                        .await
                        .map_err(std::io::Error::other)?;
                        crate::observability::metrics::MetricsManager::record_dlq_published();
                        Logger::sys_warn_with_fields(
                            "results.contract",
                            contract_error.code,
                            "Invalid result was durably quarantined before source settlement",
                            "",
                            LogFields {
                                event_id: Some(&event_id.to_string()),
                                kafka_topic: Some(&record.topic),
                                kafka_partition: Some(record.partition),
                                kafka_offset: Some(record.offset),
                                outcome: Some("quarantined"),
                                ..LogFields::default()
                            },
                        );
                        self.kafka
                            .commit(
                                &consumer,
                                &record.topic,
                                record.partition,
                                record.offset + 1,
                            )
                            .await
                            .map_err(std::io::Error::other)?;
                        continue;
                    }
                };

                let authority_parent = crate::observability::otel::OtelTracer::extract_context(
                    &result.wire.traceparent,
                    &result.wire.tracestate,
                );
                let authority_context =
                    crate::observability::otel::OtelTracer::start_span_with_parent(
                        "verify job result authority",
                        opentelemetry::trace::SpanKind::Client,
                        vec![
                            opentelemetry::KeyValue::new("db.system", "postgresql"),
                            opentelemetry::KeyValue::new("db.operation.name", "select"),
                            opentelemetry::KeyValue::new(
                                "aurora.job.id",
                                result.job_id.to_string(),
                            ),
                        ],
                        &authority_parent,
                    );
                let authority_result = apply::verify_authority(&result, &client)
                    .with_context(authority_context.clone())
                    .await;
                let authority_error = match &authority_result {
                    Ok(apply::AuthorityCheck::Verified) => None,
                    Ok(apply::AuthorityCheck::Reject(code)) => Some(*code),
                    Err(_) => Some("JOB_RESULT_AUTHORITY_QUERY_FAILED"),
                };
                crate::observability::otel::OtelTracer::finish_span(
                    &authority_context,
                    authority_error,
                );

                match authority_result? {
                    apply::AuthorityCheck::Verified => {}
                    apply::AuthorityCheck::Reject(error_code) => {
                        crate::observability::metrics::MetricsManager::record_result_rejected();
                        let event_id = quarantine::publish(
                            &self.kafka,
                            &record.topic,
                            record.partition,
                            record.offset,
                            error_code,
                            payload.as_ref(),
                        )
                        .await
                        .map_err(std::io::Error::other)?;
                        crate::observability::metrics::MetricsManager::record_dlq_published();
                        Logger::sys_warn_with_fields(
                            "results.authority",
                            error_code,
                            "Result authority fence failed and was durably quarantined",
                            "",
                            LogFields {
                                event_id: Some(&event_id.to_string()),
                                operation_id: Some(&result.job_id.to_string()),
                                kafka_topic: Some(&record.topic),
                                kafka_partition: Some(record.partition),
                                kafka_offset: Some(record.offset),
                                outcome: Some("quarantined"),
                                ..LogFields::default()
                            },
                        );
                        self.kafka
                            .commit(
                                &consumer,
                                &record.topic,
                                record.partition,
                                record.offset + 1,
                            )
                            .await
                            .map_err(std::io::Error::other)?;
                        continue;
                    }
                }

                crate::observability::metrics::MetricsManager::inc_results_received();
                let parent = crate::observability::otel::OtelTracer::extract_context(
                    &result.wire.traceparent,
                    &result.wire.tracestate,
                );
                let process_context =
                    crate::observability::otel::OtelTracer::start_span_with_parent(
                        format!("process {}", result.wire.job_topic),
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
                            opentelemetry::KeyValue::new(
                                "aurora.job.id",
                                result.job_id.to_string(),
                            ),
                            opentelemetry::KeyValue::new(
                                "aurora.job.topic",
                                result.wire.job_topic.clone(),
                            ),
                        ],
                        &parent,
                    );
                let process_result: Result<(), Box<dyn std::error::Error>> = async {
                    let _ = apply::apply_result(
                        &result,
                        &mut client,
                        &mut notification_redis,
                        &self.ownership_publisher,
                    )
                    .await?;
                    self.kafka
                        .commit(
                            &consumer,
                            &record.topic,
                            record.partition,
                            record.offset + 1,
                        )
                        .await
                        .map_err(std::io::Error::other)?;
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
                        "results.process",
                        "JOB_RESULT_PROCESS_FAILED",
                        "Result processing failed; source offset remains unsettled",
                        &error.to_string(),
                        LogFields {
                            operation_id: Some(&result.job_id.to_string()),
                            source_domain: Some(&result.wire.source_domain),
                            job_version: Some(u64::from(result.wire.job_version)),
                            kafka_topic: Some(&record.topic),
                            kafka_partition: Some(record.partition),
                            kafka_offset: Some(record.offset),
                            retryable: Some(true),
                            outcome: Some("failed"),
                            ..LogFields::default()
                        },
                    );
                    // Preserve partition ordering: never process a higher record
                    // after an unsettled lower offset.
                    return Err(error);
                }
            }
        }
    }
}
