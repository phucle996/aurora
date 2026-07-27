use crate::contracts::storage as storage_proto;
use crate::observability::logger::Logger;
use prost::Message;

// [COMMENT]: Resolve bucket creation trong một transaction.
// SUCCEEDED: UPDATE outbox thành SUCCEEDED. Ownership delivery được reconstruct từ
// chính durable row này sau commit; không ghi thêm một lifecycle outbox thứ hai.
// PROCESSING: UPDATE outbox thành PROCESSING.
// FAILED: UPDATE outbox thành FAILED + DELETE bucket record (clean rollback cho retry với cùng tên).
//
// Guard idempotent: WHERE status IN ('PENDING', 'PROCESSING') ngăn re-apply khi retry SUCCEEDED.
pub async fn resolve_bucket_creation_tx(
    tx: &tokio_postgres::Transaction<'_>,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    Logger::sys_info(
        "storage_db.resolve_bucket_creation",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Phase 1 fix: SUCCEEDED UPDATE outbox (không DELETE).
        // Record được giữ lại 30 ngày cho audit và crash recovery.
        // Guard: WHERE status IN ('PENDING','PROCESSING') đảm bảo idempotent khi retry.
        let row_opt = tx
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'SUCCEEDED', \
                     completed_at = NOW(), \
                     updated_at = NOW() \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id, owner_id, owner_type, payload, zone_id",
                &[&job_uuid, &job_topic],
            )
            .await?;

        row_opt
    } else if status == "PROCESSING" {
        // [COMMENT]: Khi job đang chạy, chỉ cập nhật trạng thái outbox sang 'PROCESSING'
        tx
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        // [COMMENT]: FAILED: UPDATE outbox + DELETE bucket record để cho phép retry với cùng tên bucket.
        // Lifecycle event KHÔNG được insert khi FAILED — chỉ SUCCEEDED mới phát event.
        tx
            .query_opt(
                "WITH updated_outbox AS ( \
                     UPDATE storage.storage_outbox_records \
                     SET status = $1, \
                         completed_at = NOW(), \
                         updated_at = NOW(), \
                         error_code = $2, \
                         error_message = $3 \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                     RETURNING actor_user_id::text, job_topic, trace_id, resource_id \
                 ), \
                 deleted_personal AS ( \
                     DELETE FROM storage.personal_buckets \
                     WHERE id::text = (SELECT resource_id FROM updated_outbox) \
                       AND (SELECT job_topic FROM updated_outbox) = 'storage.bucket.create' \
                     RETURNING id \
                 ), \
                 deleted_tenant AS ( \
                     DELETE FROM storage.tenant_buckets \
                     WHERE id::text = (SELECT resource_id FROM updated_outbox) \
                       AND (SELECT job_topic FROM updated_outbox) = 'storage.bucket.create' \
                     RETURNING id \
                 ) \
                 SELECT * FROM updated_outbox",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row_opt)
}

// [COMMENT]: Xử lý khép lại vòng đời của job resize Bucket (phục hồi quota cũ trên FAILURE).
pub async fn resolve_bucket_resize(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_bucket_resize",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Resize Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khi job resize thành công, cập nhật status sang SUCCEEDED và set completed_at.
        // Giữ outbox record 30 ngày cho audit/retention cleanup worker tự động dọn dẹp (không DELETE trực tiếp).
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'SUCCEEDED', \
                     completed_at = NOW(), \
                     updated_at = NOW() \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        // [COMMENT]: Khi job dang chay, cap nhat status outbox sang PROCESSING
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        // [COMMENT]: Khi job thất bại (FAILED/error):
        // 1. SELECT lấy payload của outbox record hiện tại để lấy quota cũ (current_quota_bytes)
        let outbox_row = pg_client
            .query_opt(
                "SELECT payload FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2",
                &[&job_uuid, &job_topic],
            )
            .await?;

        if let Some(r) = outbox_row {
            let payload_bytes: Vec<u8> = r.get(0);
            if let Ok(sync_data) = storage_proto::BucketResizeSync::decode(payload_bytes.as_slice())
            {
                // 2. Rollback quota cũ về DB dựa vào physical name prefix
                if sync_data.name.starts_with("ws-") {
                    let _ = pg_client
                        .execute(
                            "UPDATE storage.personal_buckets \
                             SET capacity_quota_bytes = $1, updated_at = NOW() \
                             WHERE name = $2",
                            &[&sync_data.current_quota_bytes, &sync_data.name],
                        )
                        .await;
                } else if sync_data.name.starts_with("tn-") {
                    let _ = pg_client
                        .execute(
                            "UPDATE storage.tenant_buckets \
                             SET capacity_quota_bytes = $1, updated_at = NOW() \
                             WHERE name = $2",
                            &[&sync_data.current_quota_bytes, &sync_data.name],
                        )
                        .await;
                }
            }
        }

        // 3. Cập nhật outbox record sang status FAILED kèm mã lỗi
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     completed_at = NOW(), \
                     updated_at = NOW(), \
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

// [COMMENT]: Resolve bucket deletion trong một transaction.
// SUCCEEDED:
//   1. Capture owner/name/zone từ DB TRƯỚC khi DELETE (sau DELETE không còn data).
//   2. DELETE credentials và bucket.
//   3. UPDATE job outbox thành SUCCEEDED (không DELETE).
//   4. Durable storage outbox giữ owner/payload/zone_id để ownership publisher
//      reconstruct event sau commit, kể cả resource row đã bị xóa.
// PROCESSING: UPDATE outbox sang PROCESSING.
// FAILED: UPDATE outbox sang FAILED, giữ nguyên resource.
pub async fn resolve_bucket_deletion_tx(
    tx: &tokio_postgres::Transaction<'_>,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    Logger::sys_info(
        "storage_db.resolve_bucket_deletion",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Delete Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khóa outbox trước, đọc owner_type rồi mới chọn đúng bảng resource.
        // Không được update SUCCEEDED ở nhánh PERSONAL trước rồi mới fallback TENANT vì nhánh đầu
        // có thể consume outbox nhưng không xóa được resource, làm mất lifecycle event.
        let outbox = tx
            .query_opt(
                "SELECT resource_id, owner_type, payload, zone_id \
                 FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2 \
                   AND status IN ('PENDING', 'PROCESSING') \
                 FOR UPDATE",
                &[&job_uuid, &job_topic],
            )
            .await?;

        let Some(outbox) = outbox else {
            return Ok(None);
        };
        let resource_id = uuid::Uuid::parse_str(outbox.get::<_, String>(0).as_str())?;
        let owner_type: String = outbox.get(1);
        let payload: Vec<u8> = outbox.get(2);
        let zone_id: uuid::Uuid = outbox.get(3);
        let sync_data = storage_proto::BucketDeleteSync::decode(payload.as_slice())?;
        if sync_data.name.trim().is_empty() {
            return Err("bucket delete outbox payload has an empty name".into());
        }
        if zone_id.is_nil() {
            return Err("bucket delete outbox has a nil zone_id".into());
        }

        // Validate the immutable ownership source before deletion. A failure
        // rolls the resource deletion and terminal outbox transition back together.
        match owner_type.as_str() {
            "PERSONAL" => {
                tx.execute(
                    "DELETE FROM storage.personal_credentials WHERE bucket_id = $1",
                    &[&resource_id],
                )
                .await?;
                tx.execute(
                    "DELETE FROM storage.personal_buckets WHERE id = $1",
                    &[&resource_id],
                )
                .await?;
            }
            "TENANT" => {
                tx.execute(
                    "DELETE FROM storage.tenant_credentials WHERE bucket_id = $1",
                    &[&resource_id],
                )
                .await?;
                tx.execute(
                    "DELETE FROM storage.tenant_buckets WHERE id = $1",
                    &[&resource_id],
                )
                .await?;
            }
            _ => return Err(format!("unsupported owner_type {}", owner_type).into()),
        }

        tx.query_opt(
            "UPDATE storage.storage_outbox_records \
             SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW() \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
             RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
            &[&job_uuid, &job_topic],
        )
        .await?
    } else if status == "PROCESSING" {
        tx
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        // [COMMENT]: FAILED: giữ nguyên resource, UPDATE outbox sang FAILED
        tx
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     completed_at = NOW(), \
                     updated_at = NOW(), \
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
