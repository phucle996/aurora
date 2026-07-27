use crate::contracts::hypervisor as hypervisor_proto;
use prost::Message;

pub async fn apply_image_result(
    pg_client: &mut tokio_postgres::Client,
    job_id: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
    result_payload: &[u8],
    result_payload_schema_version: u32,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    let tx = pg_client.transaction().await?;
    // Custom PostgreSQL enums cross this driver boundary as text so every JO
    // connection does not need mutable per-schema type registration.
    let authority = tx
        .query_opt(
            "SELECT outbox.resource_id, outbox.status::text, image.revision, image.sha256 \
             FROM hypervisor.hypervisor_outbox_records outbox \
             JOIN hypervisor.image_artifacts image ON image.id::text = outbox.resource_id \
             WHERE outbox.event_id = $1 AND outbox.job_topic = $2 \
             FOR UPDATE OF outbox, image",
            &[&job_id, &job_topic],
        )
        .await?;
    let Some(authority) = authority else {
        tx.rollback().await?;
        return Ok(None);
    };
    let resource_id: String = authority.get(0);
    let current_status: String = authority.get(1);
    let revision: i64 = authority.get(2);
    let sha256: Vec<u8> = authority.get(3);
    if !matches!(current_status.as_str(), "PENDING" | "PROCESSING") {
        tx.rollback().await?;
        return Ok(None);
    }
    let image_id = uuid::Uuid::parse_str(&resource_id)?;

    let row = match (job_topic, status) {
        (_, "PROCESSING") => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("PROCESSING image result must not carry a payload".into());
            }
            let image_state = if job_topic == "hypervisor.image.delete" {
                "DELETING"
            } else {
                "IMPORTING"
            };
            tx.query_opt(
                "WITH updated_image AS ( \
                     UPDATE hypervisor.image_artifacts \
                     SET state = $1::text::hypervisor.hypervisor_image_state, \
                         error_code = NULL, error_message = NULL, updated_at = NOW() \
                     WHERE id = $2 \
                     RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'PROCESSING', error_code = NULL, error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $3 AND job_topic = $4 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&image_state, &image_id, &job_id, &job_topic],
            )
            .await?
        }
        ("hypervisor.image.import", "SUCCEEDED") => {
            if result_payload_schema_version != 1 {
                return Err("hypervisor image import payload schema version is unsupported".into());
            }
            let result = hypervisor_proto::ImageImportResultV1::decode(result_payload)?;
            if result.schema_version != 1
                || result.image_id.as_slice() != image_id.as_bytes()
                || result.revision != u64::try_from(revision)?
                || result.sha256 != sha256
                || result.provider_template_vmid == 0
            {
                return Err(
                    "hypervisor image import result does not match the authoritative artifact"
                        .into(),
                );
            }
            let provider_template_vmid = i64::try_from(result.provider_template_vmid)?;
            tx.query_opt(
                "WITH updated_image AS ( \
                     UPDATE hypervisor.image_artifacts \
                     SET state = 'AVAILABLE', provider_template_vmid = $1, \
                         error_code = NULL, error_message = NULL, available_at = COALESCE(available_at, NOW()), \
                         updated_at = NOW() \
                     WHERE id = $2 \
                     RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                     error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $3 AND job_topic = $4 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[
                    &provider_template_vmid,
                    &image_id,
                    &job_id,
                    &job_topic,
                ],
            )
            .await?
        }
        ("hypervisor.image.delete", "SUCCEEDED") => {
            if result_payload_schema_version != 1 {
                return Err("hypervisor image delete payload schema version is unsupported".into());
            }
            let result = hypervisor_proto::ImageDeleteResultV1::decode(result_payload)?;
            if result.schema_version != 1
                || result.image_id.as_slice() != image_id.as_bytes()
                || result.revision != u64::try_from(revision)?
                || result.sha256 != sha256
            {
                return Err(
                    "hypervisor image delete result does not match the authoritative artifact"
                        .into(),
                );
            }
            // Hard delete is deliberate: the durable hypervisor outbox keeps
            // the audit/fence while the artifact row disappears after Zone ACK.
            tx.query_opt(
                "WITH deleted_image AS ( \
                     DELETE FROM hypervisor.image_artifacts WHERE id = $1 RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                     error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $2 AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&image_id, &job_id, &job_topic],
            )
            .await?
        }
        (_, "FAILED") => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("FAILED image result must not carry a payload".into());
            }
            tx.query_opt(
                "WITH updated_image AS ( \
                     UPDATE hypervisor.image_artifacts \
                     SET state = 'FAILED', error_code = $1, error_message = $2, updated_at = NOW() \
                     WHERE id = $3 \
                     RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'FAILED', completed_at = NOW(), error_code = $1, \
                     error_message = $2, updated_at = NOW() \
                 WHERE event_id = $4 AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&error_code, &error_message, &image_id, &job_id, &job_topic],
            )
            .await?
        }
        (_, _) => {
            return Err(format!("unsupported hypervisor image result status '{status}'").into())
        }
    };
    tx.commit().await?;
    Ok(row)
}
