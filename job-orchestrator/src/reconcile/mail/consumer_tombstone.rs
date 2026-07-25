use super::publish_mail_projection_command;
use crate::contracts::mail::{MailConsumerDeleteV1, MailEventMetadataV1};
use crate::infra::kafka::KafkaTransport;
use chrono::{DateTime, Utc};
use prost::Message;
use uuid::Uuid;

pub(super) async fn reconcile_personal_consumer_tombstones(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    kafka: &KafkaTransport,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg
        .query(
            "SELECT consumer_id,config_version,delete_event_id,tombstoned_at \
             FROM mail.personal_mail_consumer_projection_tombstones \
             WHERE zone_id=$1 AND consumer_id::text > $2 \
             ORDER BY consumer_id LIMIT $3",
            &[&zone_id, &cursor_id, &limit],
        )
        .await?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let config_version: i64 = row.get(1);
        let event_id: Uuid = row.get(2);
        let tombstoned_at: DateTime<Utc> = row.get(3);
        let event = MailConsumerDeleteV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: tombstoned_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            consumer_id: consumer_id.as_bytes().to_vec(),
            config_version: config_version as u64,
            drain_timeout_seconds: 0,
            reason: "hard-delete-reconciliation".to_string(),
        };
        // [COMMENT]: Business row đã hard-delete; durable tombstone là nguồn duy nhất có quyền
        // fence Zone KV sau khi outbox gốc hết retention.
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            "mail.consumer.delete",
            &consumer_id.to_string(),
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}

pub(super) async fn reconcile_tenant_consumer_tombstones(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    kafka: &KafkaTransport,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    // [COMMENT]: Tenant tombstone có cursor riêng để một namespace lớn không làm đói namespace còn lại.
    let rows = pg
        .query(
            "SELECT consumer_id,config_version,delete_event_id,tombstoned_at \
             FROM mail.tenant_mail_consumer_projection_tombstones \
             WHERE zone_id=$1 AND consumer_id::text > $2 \
             ORDER BY consumer_id LIMIT $3",
            &[&zone_id, &cursor_id, &limit],
        )
        .await?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let config_version: i64 = row.get(1);
        let event_id: Uuid = row.get(2);
        let tombstoned_at: DateTime<Utc> = row.get(3);
        let event = MailConsumerDeleteV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: tombstoned_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            consumer_id: consumer_id.as_bytes().to_vec(),
            config_version: config_version as u64,
            drain_timeout_seconds: 0,
            reason: "hard-delete-reconciliation".to_string(),
        };
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            "mail.consumer.delete",
            &consumer_id.to_string(),
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}
