use crate::observability::logger::Logger;

// Access preparation has no business aggregate mutation. It only settles the
// durable command fence after Dataplane has applied the matching Zone record.
pub async fn resolve_access_prepare(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_access_prepare",
        &format!(
            "Settling storage access preparation: {} -> {}",
            job_uuid, status
        ),
    );

    let row = if status == "SUCCEEDED" {
        pg_client
            .query_opt(
                "DELETE FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, error_code = NULL, error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'FAILED', error_code = $1, error_message = $2 \
                 WHERE event_id = $3::uuid AND job_topic = $4 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row)
}
