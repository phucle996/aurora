use super::publish_mail_projection_command;
use crate::infra::kafka::KafkaTransport;
use uuid::Uuid;

pub(super) async fn reconcile_tenant_consumers(
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
            "SELECT c.id,p.event_id,p.job_topic,p.payload \
         FROM mail.tenant_mail_consumers c \
         JOIN hierarchy.tenant_workspaces w ON w.id=c.workspace_id \
         JOIN LATERAL ( \
             SELECT event_id,job_topic,payload FROM mail.mail_protected_projections \
             WHERE resource_kind='consumer' AND resource_id=c.id::text \
               AND job_topic='mail.consumer.upsert' \
             ORDER BY source_outbox_id DESC LIMIT 1 \
         ) p ON TRUE \
         WHERE w.zone_id=$1 AND c.id::text > $2 ORDER BY c.id LIMIT $3",
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
