use crate::observability::logger::Logger;

// [COMMENT]: Xử lý khép lại vòng đời của các Job Object (Xóa Outbox record trên SUCCESS, hoặc cập nhật PROCESSING/FAILED).
pub async fn resolve_object_job(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_object_job",
        &format!(
            "Khép lại vòng đời Outbox cho Object Job: {} -> {}",
            job_uuid, status
        ),
    );

    let row_opt = if status == "SUCCEEDED" {
        // [COMMENT]: Khi job thành công, thực hiện xóa cứng outbox record để dọn dẹp CSDL trung tâm, RETURNING các thông tin định danh
        pg_client
            .query_opt(
                "DELETE FROM storage.storage_outbox_records \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        // [COMMENT]: Khi job đang chạy, cập nhật trạng thái sang PROCESSING
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
        // [COMMENT]: Khi job thất bại, cập nhật trạng thái sang FAILED và lưu vết mã lỗi
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'FAILED', \
                     error_code = $1, \
                     error_message = $2 \
                 WHERE event_id = $3::uuid AND job_topic = $4 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row_opt)
}
