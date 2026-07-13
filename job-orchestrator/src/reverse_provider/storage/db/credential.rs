use crate::observability::logger::Logger;

// [COMMENT]: Xử lý khép lại vòng đời của job xóa Credential (xóa Outbox và Credential DB trên kết quả thành công, hoặc cập nhật FAILED).
pub async fn resolve_credential_deletion(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_credential_deletion",
        &format!(
            "Khép lại vòng đời Outbox cho Credential Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khi job thành công: Xóa outbox record trước, sau đó xóa cứng credential ở cả personal_credentials và tenant_credentials.
        // CTE đảm bảo tính toàn vẹn (xóa ở resource MinIO thành công mới thực hiện xóa sạch khỏi DB).
        pg_client
            .query_opt(
                "WITH deleted_outbox AS ( \
                     DELETE FROM storage.storage_outbox_records \
                     WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                     RETURNING user_id, job_topic, trace_id, resource_id \
                 ), \
                 deleted_personal AS ( \
                     DELETE FROM storage.personal_credentials \
                     WHERE id = (SELECT resource_id::uuid FROM deleted_outbox) \
                     RETURNING id \
                 ), \
                 deleted_tenant AS ( \
                     DELETE FROM storage.tenant_credentials \
                     WHERE id = (SELECT resource_id::uuid FROM deleted_outbox) \
                     RETURNING id \
                 ) \
                 SELECT * FROM deleted_outbox",
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
        // [COMMENT]: Khi job thất bại: Chỉ cập nhật trạng thái Outbox sang FAILED kèm mã lỗi.
        // Tuyệt đối không xóa Credential trong database để người dùng vẫn có thể thấy/sử dụng hoặc thực hiện retry xóa lại.
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

// [COMMENT]: Xử lý khép lại vòng đời của job tạo Credential.
// - SUCCEEDED: Xóa outbox record — credential đã tồn tại hợp lệ trong MinIO và DB, không cần làm gì thêm.
// - PROCESSING: Cập nhật trạng thái outbox sang 'PROCESSING' để track tiến trình.
// - FAILED: Xóa đồng thời outbox record VÀ credential record khỏi DB.
//   Đảm bảo nguyên tắc atomicity: tạo thất bại tại MinIO thì DB cũng không được giữ lại record — cho phép retry với tên/key không bị conflict.
pub async fn resolve_credential_creation(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_credential_creation",
        &format!(
            "Khép lại vòng đời Outbox cho Credential Create Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khi job thành công: Xóa outbox record, credential đã được tạo thành công ở MinIO nên giữ lại trong DB.
        pg_client
            .query_opt(
                "DELETE FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING user_id, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        // [COMMENT]: Khi job đang chạy, chỉ cập nhật trạng thái outbox sang 'PROCESSING'.
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
        // [COMMENT]: Khi job thất bại: Rollback atomically — xóa outbox VÀ xóa credential khỏi DB (personal + tenant).
        // Lý do: Credential chưa được tạo thành công ở MinIO, để lại trong DB sẽ gây ra credential "ma" không dùng được.
        // Xóa sạch để user có thể retry mà không bị conflict unique constraint (access_key).
        pg_client
            .query_opt(
                "WITH deleted_outbox AS ( \
                     UPDATE storage.storage_outbox_records \
                     SET status = $1, \
                         completed_at = CURRENT_TIMESTAMP, \
                         error_code = $2, \
                         error_message = $3 \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                     RETURNING user_id, job_topic, trace_id, resource_id \
                 ), \
                 deleted_personal AS ( \
                     DELETE FROM storage.personal_credentials \
                     WHERE id = (SELECT resource_id::uuid FROM deleted_outbox) \
                     RETURNING id \
                 ), \
                 deleted_tenant AS ( \
                     DELETE FROM storage.tenant_credentials \
                     WHERE id = (SELECT resource_id::uuid FROM deleted_outbox) \
                     RETURNING id \
                 ) \
                 SELECT * FROM deleted_outbox",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row_opt)
}
