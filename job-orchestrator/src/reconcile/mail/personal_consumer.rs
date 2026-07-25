use super::{publish_mail_projection_command, CONSUMER_EVENT_NAMESPACE};
use crate::contracts::mail::{
    KafkaStreamPayloadV1, MailConsumerDesiredState, MailConsumerUpsertV1, MailEventMetadataV1,
    MailStreamSourceV1, MailStreamType, NatsJetStreamPayloadV1, RabbitMqPayloadV1,
    RedisStreamPayloadV1,
};
use crate::infra::kafka::KafkaTransport;
use chrono::{DateTime, Utc};
use prost::Message;
use uuid::Uuid;

pub(super) async fn reconcile_personal_consumers(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    kafka: &KafkaTransport,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT c.id,c.broker_resource_id,c.source_config_envelope,c.topic,c.consumer_group,\
                c.template_id,c.template_version,c.sender_profile_id,c.sender_version,c.desired_state::text,\
                c.parallelism,c.config_version,c.config_sha256,c.updated_at,c.source_type::text \
         FROM mail.personal_mail_consumers c JOIN hierarchy.personal_workspaces w ON w.id=c.workspace_id \
         WHERE w.zone_id=$1 AND c.id::text > $2 ORDER BY c.id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let namespace = Uuid::parse_str(CONSUMER_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let config_version: i64 = row.get(11);
        let desired_state: String = row.get(9);
        let updated_at: DateTime<Utc> = row.get(13);
        let (event_id, topic, payload) = {
            let event_id = Uuid::new_v5(
                &namespace,
                format!("consumer:{consumer_id}:{config_version}:upsert:{zone_id}").as_bytes(),
            );
            let broker_resource_id: Uuid = row.get(1);
            let source_config_envelope = row.get::<_, Vec<u8>>(2);
            let source_name = row.get::<_, String>(3);
            let consumer_name = row.get::<_, String>(4);
            // [COMMENT]: Reconciler match discriminator một lần và tái tạo đúng payload suite; ciphertext vẫn opaque với JO.
            let (stream_type, stream_payload) = match row.get::<_, String>(14).as_str() {
                "kafka" => (
                    MailStreamType::Kafka,
                    KafkaStreamPayloadV1 {
                        source_config_envelope,
                        topic: source_name,
                        consumer_group: consumer_name,
                    }
                    .encode_to_vec(),
                ),
                "redis_stream" => (
                    MailStreamType::RedisStream,
                    RedisStreamPayloadV1 {
                        source_config_envelope,
                        stream_key: source_name,
                        consumer_group: consumer_name,
                    }
                    .encode_to_vec(),
                ),
                "nats_jetstream" => (
                    MailStreamType::NatsJetstream,
                    NatsJetStreamPayloadV1 {
                        source_config_envelope,
                        stream_name: source_name,
                        durable_name: consumer_name,
                    }
                    .encode_to_vec(),
                ),
                "rabbitmq" => (
                    MailStreamType::Rabbitmq,
                    RabbitMqPayloadV1 {
                        source_config_envelope,
                        queue_name: source_name,
                        consumer_tag_prefix: consumer_name,
                    }
                    .encode_to_vec(),
                ),
                source_type => {
                    return Err(format!("unsupported mail source_type: {source_type}").into());
                }
            };
            let event = MailConsumerUpsertV1 {
                metadata: Some(MailEventMetadataV1 {
                    event_id: event_id.as_bytes().to_vec(),
                    schema_version: 1,
                    occurred_at_unix_ms: updated_at.timestamp_millis(),
                    traceparent: String::new(),
                    producer: "job-orchestrator-mail-reconciler".to_string(),
                }),
                consumer_id: consumer_id.as_bytes().to_vec(),
                config_version: config_version as u64,
                stream: Some(MailStreamSourceV1 {
                    stream_type: stream_type as i32,
                    payload_schema_version: 1,
                    broker_resource_id: broker_resource_id.as_bytes().to_vec(),
                    payload: stream_payload,
                }),
                template_id: row.get(5),
                template_version: row.get::<_, i64>(6) as u64,
                sender_profile_id: row.get(7),
                sender_version: row.get::<_, i64>(8) as u64,
                desired_state: if desired_state == "enabled" {
                    MailConsumerDesiredState::Enabled as i32
                } else {
                    MailConsumerDesiredState::Paused as i32
                },
                parallelism: row.get::<_, i32>(10) as u32,
                config_sha256: row.get(12),
            };
            (event_id, "mail.consumer.upsert", event.encode_to_vec())
        };
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            topic,
            &consumer_id.to_string(),
            &payload,
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}
