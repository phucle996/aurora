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
    nats_client: async_nats::Client,
}

impl JobResultConsumer {
    pub fn new(
        config: Config,
        kafka: Arc<KafkaTransport>,
        nats_client: async_nats::Client,
    ) -> Self {
        Self {
            config,
            kafka,
            nats_client,
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
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
                let valid_result =
                    l1_dispatcher::job_proto::JobExecutionResultProto::decode(payload.as_ref())
                        .is_ok_and(|result| {
                            result.job_id.len() == 16
                                && !result.job_topic.trim().is_empty()
                                && matches!(result.source_domain.as_str(), "MAIL" | "STORAGE")
                                && matches!(
                                    result.result_status.as_str(),
                                    "PROCESSING" | "SUCCEEDED" | "FAILED"
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
                };
                match l1_dispatcher::dispatch_result(
                    payload.as_ref(),
                    &mut client,
                    &self.nats_client,
                )
                .await
                {
                    Ok(()) => {
                        self.kafka
                            .commit(
                                &consumer,
                                &record.topic,
                                record.partition,
                                record.offset + 1,
                            )
                            .await
                            .map_err(std::io::Error::other)?;
                    }
                    Err(error) => {
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
}
