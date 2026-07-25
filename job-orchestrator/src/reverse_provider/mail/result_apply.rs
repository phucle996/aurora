use super::service::{consumer_result, template_result};

/// Định tuyến kết quả Mail theo topic đã được đóng dấu trong authoritative outbox.
/// SQL/lifecycle nằm trọn trong từng service để dispatcher không che transaction boundary.
pub async fn apply_mail_result(
    pg_client: &mut tokio_postgres::Client,
    event_id: uuid::Uuid,
    job_topic: &str,
    status: &str,
    attempt: u32,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> {
    if !matches!(status, "PROCESSING" | "SUCCEEDED" | "FAILED") {
        return Err(format!("unsupported Mail result status '{status}'").into());
    }
    if attempt > i32::MAX as u32 {
        return Err("Mail result attempt exceeds PostgreSQL INT".into());
    }

    match job_topic {
        "mail.consumer.upsert" => {
            consumer_result::apply_upsert_result(
                pg_client,
                event_id,
                status,
                attempt,
                error_code,
                error_message,
            )
            .await
        }
        "mail.consumer.delete" => {
            consumer_result::apply_delete_result(
                pg_client,
                event_id,
                status,
                attempt,
                error_code,
                error_message,
            )
            .await
        }
        "mail.template.version_published" | "mail.template.deleted" => {
            template_result::apply_result(
                pg_client,
                event_id,
                job_topic,
                status,
                attempt,
                error_code,
                error_message,
            )
            .await
        }
        _ => Err(format!("unsupported Mail job_topic '{job_topic}'").into()),
    }
}
