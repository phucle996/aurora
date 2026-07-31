use super::publish_mail_projection_command;
use crate::infra::kafka::KafkaTransport;
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
            "SELECT t.consumer_id,p.event_id,p.job_topic,p.payload \
             FROM mail.personal_mail_consumer_projection_tombstones t \
             JOIN mail.mail_protected_projections p ON p.event_id=t.delete_event_id \
             WHERE t.zone_id=$1 AND t.consumer_id::text > $2 \
             ORDER BY consumer_id LIMIT $3",
            &[&zone_id, &cursor_id, &limit],
        )
        .await?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let event_id: Uuid = row.get(1);
        let topic: String = row.get(2);
        let payload: Vec<u8> = row.get(3);
        // [COMMENT]: Business row đã hard-delete; durable tombstone là nguồn duy nhất có quyền
        // fence Zone KV sau khi outbox gốc hết retention.
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            &topic,
            &consumer_id.to_string(),
            &payload,
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
            "SELECT t.consumer_id,p.event_id,p.job_topic,p.payload \
             FROM mail.tenant_mail_consumer_projection_tombstones t \
             JOIN mail.mail_protected_projections p ON p.event_id=t.delete_event_id \
             WHERE t.zone_id=$1 AND t.consumer_id::text > $2 \
             ORDER BY consumer_id LIMIT $3",
            &[&zone_id, &cursor_id, &limit],
        )
        .await?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let event_id: Uuid = row.get(1);
        let topic: String = row.get(2);
        let payload: Vec<u8> = row.get(3);
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            &topic,
            &consumer_id.to_string(),
            &payload,
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}
