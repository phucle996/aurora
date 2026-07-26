use super::{access, bucket, credential};
use crate::observability::logger::Logger;

// Phân phối kết quả Storage job tới đúng transaction owner.
// Với create và delete bucket, transaction khép durable job state trước khi
// ownership fast path được phép đọc lại authoritative outbox row.
pub async fn apply_storage_result(
    pg_client: &mut tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    Logger::sys_info(
        "storage.result_apply",
        &format!("Applying Storage result for job_topic='{job_topic}'"),
    );

    match job_topic {
        "storage.bucket.create" => {
            // Ownership is derived only after this transaction commits. A Redis
            // outage leaves ownership_published_at NULL for the recovery relay.
            let tx = pg_client.transaction().await?;
            let row_opt = bucket::resolve_bucket_creation_tx(
                &tx,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await?;
            tx.commit().await?;
            Ok(row_opt)
        }
        "storage.credential.create" => credential::resolve_credential_creation(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        "storage.credential.delete" => credential::resolve_credential_deletion(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        "storage.bucket.resize" => bucket::resolve_bucket_resize(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        "storage.bucket.delete" => {
            // Delete captures owner/name/Zone on the outbox before the resource
            // disappears; that row is the only ownership recovery source.
            let tx = pg_client.transaction().await?;
            let row_opt = bucket::resolve_bucket_deletion_tx(
                &tx,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await?;
            tx.commit().await?;
            Ok(row_opt)
        }
        // Access-session preparation has no business aggregate to mutate. The
        // outbox row is only a durable command fence and is settled by the
        // dedicated idempotent access-preparation lifecycle.
        "storage.access.prepare" => access::resolve_access_prepare(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),

        _ => {
            Logger::sys_warn(
                "storage.result_apply",
                &format!(
                    "Không tìm thấy handler phù hợp cho Storage Job Topic: {}",
                    job_topic
                ),
                "",
            );
            Ok(None)
        }
    }
}
