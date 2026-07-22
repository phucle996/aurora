use super::{xadd_mail_projection_command, TENANT_TEMPLATE_EVENT_NAMESPACE};
use crate::reverse_provider::mail::runtime_proto::{
    MailEventMetadataV1, MailTemplateDeletedV1, MailTemplateVersionPublishedV1,
};
use chrono::{DateTime, Utc};
use prost::Message;
use uuid::Uuid;

pub(super) async fn reconcile_tenant_template_versions(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    cursor_version: i64,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at \
         FROM mail.tenant_mail_templates t \
         JOIN mail.tenant_mail_template_versions v ON v.template_id=t.id \
         JOIN hierarchy.tenant_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND (t.id, v.version) > ($2, $3) \
         ORDER BY t.id, v.version LIMIT $4",
        &[&zone_id, &cursor_id, &cursor_version, &limit],
    ).await?;
    let namespace = Uuid::parse_str(TENANT_TEMPLATE_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    let mut last_version = 0;
    for row in &rows {
        let template_id: String = row.get(0);
        let version: i64 = row.get(1);
        let created_at: DateTime<Utc> = row.get(5);
        let event_id = Uuid::new_v5(
            &namespace,
            format!("template:{template_id}:{version}:publish:{zone_id}").as_bytes(),
        );
        let event = MailTemplateVersionPublishedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: created_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: version as u64,
            template_version: version as u64,
            subject_template: row.get(2),
            html_template: row.get(3),
            content_sha256: row.get(4),
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.version_published",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
        last_version = version;
    }
    Ok((rows.len(), last_id, last_version))
}

pub(super) async fn reconcile_tenant_template_tombstones(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.template_id,t.template_revision,t.last_published_version,t.event_id,t.deleted_at \
         FROM mail.tenant_mail_template_projection_tombstones t \
         JOIN hierarchy.tenant_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND t.template_id>$2 ORDER BY t.template_id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let mut last_id = String::new();
    for row in &rows {
        let template_id: String = row.get(0);
        let revision: i64 = row.get(1);
        let event_id: Uuid = row.get(3);
        let deleted_at: DateTime<Utc> = row.get(4);
        let event = MailTemplateDeletedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: deleted_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: revision as u64,
            last_published_version: row.get::<_, i64>(2) as u64,
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.deleted",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
    }
    Ok((rows.len(), last_id, 0))
}
