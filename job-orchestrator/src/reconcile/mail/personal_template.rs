use super::publish_mail_projection_command;
use crate::infra::kafka::KafkaTransport;
use uuid::Uuid;

// Cursor identity and version remain separate to make ordering fences explicit.
#[allow(clippy::too_many_arguments)]
pub(super) async fn reconcile_personal_template_versions(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    kafka: &KafkaTransport,
    zone_id: Uuid,
    cursor_id: &str,
    cursor_version: i64,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg
        .query(
            "SELECT t.id,v.version,p.event_id,p.job_topic,p.payload \
         FROM mail.personal_mail_templates t \
         JOIN mail.personal_mail_template_versions v ON v.template_id=t.id \
         JOIN mail.mail_protected_projections p ON p.event_id=v.event_id \
         JOIN hierarchy.personal_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND v.version <= t.current_version \
           AND (t.id, v.version) > ($2, $3) \
         ORDER BY t.id, v.version LIMIT $4",
            &[&zone_id, &cursor_id, &cursor_version, &limit],
        )
        .await?;
    let mut last_id = String::new();
    let mut last_version = 0;
    for row in &rows {
        let template_id: String = row.get(0);
        let version: i64 = row.get(1);
        let event_id: Uuid = row.get(2);
        let topic: String = row.get(3);
        let payload: Vec<u8> = row.get(4);
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            &topic,
            &template_id,
            &payload,
            generation,
        )
        .await?;
        last_id = template_id;
        last_version = version;
    }
    Ok((rows.len(), last_id, last_version))
}

pub(super) async fn reconcile_personal_template_tombstones(
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
            "SELECT t.template_id,p.event_id,p.job_topic,p.payload \
         FROM mail.personal_mail_template_projection_tombstones t \
         JOIN mail.mail_protected_projections p ON p.event_id=t.event_id \
         JOIN hierarchy.personal_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND t.template_id>$2 ORDER BY t.template_id LIMIT $3",
            &[&zone_id, &cursor_id, &limit],
        )
        .await?;
    let mut last_id = String::new();
    for row in &rows {
        let template_id: String = row.get(0);
        let event_id: Uuid = row.get(1);
        let topic: String = row.get(2);
        let payload: Vec<u8> = row.get(3);
        publish_mail_projection_command(
            redis_conn,
            kafka,
            zone_id,
            event_id,
            &topic,
            &template_id,
            &payload,
            generation,
        )
        .await?;
        last_id = template_id;
    }
    Ok((rows.len(), last_id, 0))
}
