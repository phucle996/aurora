use super::{access, bucket, credential};
use crate::observability::logger::Logger;

/// Storage result input only; database capability stays a separate argument.
pub struct StorageResultRequest<'a> {
    pub job_id: uuid::Uuid,
    pub job_topic: &'a str,
    pub status: &'a str,
    pub error_code: Option<&'a str>,
    pub error_message: Option<&'a str>,
    pub result_payload: &'a [u8],
    pub result_payload_schema_version: u32,
}

// Phân phối kết quả Storage job tới đúng transaction owner.
// Với create và delete bucket, transaction khép durable job state trước khi
// ownership fast path được phép đọc lại authoritative outbox row.
pub async fn apply_storage_result(
    pg_client: &tokio_postgres::Client,
    request: StorageResultRequest<'_>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    let StorageResultRequest {
        job_id: job_uuid,
        job_topic,
        status,
        error_code,
        error_message,
        result_payload,
        result_payload_schema_version,
    } = request;

    Logger::sys_info(
        "storage.result_apply",
        &format!("Applying Storage result for job_topic='{job_topic}'"),
    );

    match job_topic {
        "storage.bucket.create" => bucket::resolve_bucket_creation(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
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
        "storage.bucket.resize" => {
            bucket::resolve_bucket_resize(
                pg_client,
                job_uuid,
                status,
                error_code,
                error_message,
                result_payload,
                result_payload_schema_version,
            )
            .await
        }
        "storage.bucket.versioning" => {
            bucket::resolve_bucket_versioning(
                pg_client,
                job_uuid,
                status,
                error_code,
                error_message,
                result_payload,
                result_payload_schema_version,
            )
            .await
        }
        "storage.bucket.lifecycle" => {
            bucket::resolve_bucket_lifecycle(
                pg_client,
                job_uuid,
                status,
                error_code,
                error_message,
                result_payload,
                result_payload_schema_version,
            )
            .await
        }
        "storage.bucket.delete" => bucket::resolve_bucket_deletion(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        // Access-session preparation and commercial admission sync have no business aggregate
        // to mutate. The outbox row is a durable command fence settled by the idempotent lifecycle.
        "storage.access.prepare" | "storage.bucket.commercial_admission" => {
            access::resolve_access_prepare(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
            .map_err(Into::into)
        }

        _ => {
            Logger::sys_warn(
                "storage.result_apply",
                &format!(
                    "Không tìm thấy handler phù hợp cho Storage Job Topic: {}",
                    job_topic
                ),
                "",
            );
            Err(format!("unsupported Storage job_topic '{job_topic}'").into())
        }
    }
}
