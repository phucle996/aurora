use super::db;
use crate::observability::logger::Logger;

// [COMMENT]: L2 Dispatcher phân phối kết quả của Storage Jobs cho đúng hàm xử lý DB tương ứng.
// Với create và delete bucket, dispatcher bắt đầu transaction để đảm bảo:
//   UPDATE job outbox + INSERT lifecycle event xảy ra trong cùng một atomic transaction.
// Các job type khác (resize, credential) vẫn dùng client thẳng.
pub async fn dispatch_storage_result(
    pg_client: &mut tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
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
            // [COMMENT]: Bắt đầu transaction để đảm bảo UPDATE outbox + INSERT lifecycle event là atomic.
            // Nếu lifecycle insert thất bại, toàn bộ transaction rollback — không có SUCCEEDED mà không có event.
            let tx = pg_client.transaction().await?;
            let row_opt = db::bucket::resolve_bucket_creation_tx(
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
        "storage.credential.create" => db::credential::resolve_credential_creation(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        "storage.credential.delete" => db::credential::resolve_credential_deletion(
            pg_client,
            job_uuid,
            job_topic,
            status,
            error_code,
            error_message,
        )
        .await
        .map_err(Into::into),
        "storage.bucket.resize" => db::bucket::resolve_bucket_resize(
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
            // [COMMENT]: Delete cũng cần transaction: capture owner → DELETE resource → UPDATE outbox → INSERT RESOURCE_DELETED
            // Thứ tự quan trọng: phải capture owner/name/zone TRƯỚC khi DELETE bucket record.
            let tx = pg_client.transaction().await?;
            let row_opt = db::bucket::resolve_bucket_deletion_tx(
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
        "storage.object.sts" => db::object::resolve_object_job(
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
