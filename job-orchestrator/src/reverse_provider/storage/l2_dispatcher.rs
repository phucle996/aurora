use super::db;
use crate::observability::logger::Logger;

// [COMMENT]: L2 Dispatcher phân phối kết quả của Storage Jobs cho đúng hàm xử lý DB tương ứng.
pub async fn dispatch_storage_result(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage.l2_dispatcher",
        &format!("Storage L2 Dispatcher: Nhận job_topic='{}'", job_topic),
    );

    // [COMMENT]: Gọi giả lập để compiler biết các Struct và Hàm được sử dụng, triệt tiêu warnings
    if false {
        db::bucket::silence_unused_proto_structs();
    }

    match job_topic {
        "storage.bucket.create" => {
            db::bucket::resolve_bucket_creation(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        "storage.credential.create" => {
            db::credential::resolve_credential_creation(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        "storage.credential.delete" => {
            db::credential::resolve_credential_deletion(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        "storage.bucket.resize" => {
            db::bucket::resolve_bucket_resize(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        "storage.bucket.delete" => {
            db::bucket::resolve_bucket_deletion(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        "storage.object.presign" => {
            db::object::resolve_object_job(
                pg_client,
                job_uuid,
                job_topic,
                status,
                error_code,
                error_message,
            )
            .await
        }
        _ => {
            Logger::sys_warn(
                "storage.l2_dispatcher",
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
