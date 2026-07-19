use crate::observability::logger::Logger;

// [COMMENT]: Cập nhật trạng thái và lưu kết quả của Mail Outbox Record vào database Postgres.
// Trả về RETURNING actor_user_id, job_topic, trace_id, resource_id phục vụ OTel và notification.
pub async fn update_outbox_record(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "mail_db.update_outbox",
        &format!(
            "Cập nhật trạng thái Outbox cho Mail Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        pg_client
            .query_opt(
                "UPDATE mail.mail_outbox_records \
                 SET status = $1, \
                     completed_at = CURRENT_TIMESTAMP, \
                     updated_at = CURRENT_TIMESTAMP, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        pg_client
            .query_opt(
                 "UPDATE mail.mail_outbox_records \
                 SET status = $1, \
                     updated_at = CURRENT_TIMESTAMP, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        pg_client
            .query_opt(
                "UPDATE mail.mail_outbox_records \
                 SET status = $1, \
                     completed_at = CURRENT_TIMESTAMP, \
                     updated_at = CURRENT_TIMESTAMP, \
                     error_code = $2, \
                     error_message = $3 \
                 WHERE event_id = $4::uuid AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row_opt)
}
