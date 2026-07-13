use crate::observability::logger::Logger;
use tokio_postgres::NoTls;

// [COMMENT]: Cập nhật dung lượng used_bytes cho bucket cá nhân trực tiếp vào DB và trả về owner_id (User ID)
pub async fn update_personal_bucket_size(
    db_url: &str,
    name: &str,
    used_bytes: i64,
) -> Result<Option<String>, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "storage_db.connection",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi cập nhật personal bucket size",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Thực hiện cập nhật và trả về owner_id của workspace sở hữu bucket bằng lệnh RETURNING
    let row = pg_client
        .query_opt(
            "UPDATE storage.personal_buckets b \
             SET used_bytes = $1, updated_at = NOW() \
             FROM hierarchy.personal_workspaces w \
             WHERE b.workspace_id = w.id AND b.name = $2 \
             RETURNING w.owner_id::text",
            &[&used_bytes, &name],
        )
        .await?;

    Ok(row.map(|r| r.get::<_, String>(0)))
}

// [COMMENT]: Cập nhật dung lượng used_bytes cho bucket doanh nghiệp trực tiếp vào DB và trả về danh sách User ID thành viên active
pub async fn update_tenant_bucket_size(
    db_url: &str,
    name: &str,
    used_bytes: i64,
) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "storage_db.connection",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi cập nhật tenant bucket size",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Sử dụng CTE cập nhật và truy vấn lấy tất cả user_id thuộc tenant có status active
    let rows = pg_client
        .query(
            "WITH updated AS ( \
                 UPDATE storage.tenant_buckets \
                 SET used_bytes = $1, updated_at = NOW() \
                 WHERE name = $2 \
                 RETURNING tenant_id \
             ) \
             SELECT m.user_id::text \
             FROM updated u \
             JOIN hierarchy.tenant_memberships m ON m.tenant_id = u.tenant_id \
             WHERE m.status = 'active'",
            &[&used_bytes, &name],
        )
        .await?;

    let user_ids = rows.iter().map(|r| r.get::<_, String>(0)).collect();
    Ok(user_ids)
}

// [COMMENT]: Xử lý khép lại vòng đời của job tạo Bucket (xóa Outbox và chuyển status bucket sang 'active' trên SUCCESS, hoặc xóa bucket trên FAILURE).
pub async fn resolve_bucket_creation(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_bucket_creation",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khi job thành công, chỉ cần xóa outbox record.
        // Bucket đã tồn tại trong DB từ lúc INSERT — không cần update status vì status column đã bị drop.
        pg_client
            .query_opt(
                "DELETE FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING user_id, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        // [COMMENT]: Khi job đang chạy, chỉ cập nhật trạng thái outbox sang 'PROCESSING'
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING user_id, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        // [COMMENT]: Khi job thất bại, cập nhật outbox đồng thời xóa hoàn toàn record bucket khỏi DB để cho phép retry đặt trùng tên và làm sạch dữ liệu
        pg_client
            .query_opt(
                "WITH updated_outbox AS ( \
                     UPDATE storage.storage_outbox_records \
                     SET status = $1, \
                         completed_at = CURRENT_TIMESTAMP, \
                         error_code = $2, \
                         error_message = $3 \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                     RETURNING user_id, job_topic, trace_id, resource_id \
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
