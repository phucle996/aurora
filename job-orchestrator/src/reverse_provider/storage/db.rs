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

// [COMMENT]: Cập nhật trạng thái và lưu kết quả của Storage Outbox Record vào database Postgres.
// Trả về RETURNING user_id, job_topic, trace_id, resource_id phục vụ OTel và phát sự kiện real-time.
pub async fn update_outbox_record(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.update_outbox",
        &format!("Cập nhật trạng thái Outbox cho Storage Job: {} -> {}", job_uuid, status),
    );

    let row_opt = if status == "SUCCEEDED" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     completed_at = CURRENT_TIMESTAMP, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING user_id, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
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
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     completed_at = CURRENT_TIMESTAMP, \
                     error_code = $2, \
                     error_message = $3 \
                 WHERE event_id = $4::uuid AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING user_id, job_topic, trace_id, resource_id",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row_opt)
}
