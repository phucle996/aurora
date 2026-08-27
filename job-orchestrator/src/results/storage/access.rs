use crate::observability::logger::Logger;

/// [COMMENT]: Xử lý khép lại vòng đời của job `storage.access.prepare`.
///
/// Bối cảnh nghiệp vụ:
/// - Khi người dùng yêu cầu upload/download file trực tiếp từ trình duyệt mà không để lộ
///   S3 Secret Key dài hạn, Controlplane phát lệnh `storage.access.prepare` qua Outbox.
/// - Zone Dataplane (`StorageAccessPrepareExecutor`) nhận job và tạo một bản ghi quyền truy cập
///   tạm thời (`StorageAccessRecord`) lưu vào Zone KV Store.
/// - Khi Dataplane thực hiện xong và gửi kết quả về Kafka:
///   + `SUCCEEDED`: Hàm này cập nhật `storage_outbox_records.status = 'SUCCEEDED'`, đánh dấu
///     phiên (Access Session) đã ACTIVE. Browser sau đó có thể lấy Transfer Ticket qua Zone Control
///     để tải/đẩy dữ liệu trực tiếp vào MinIO.
///   + `PROCESSING`: Cập nhật trạng thái outbox sang `PROCESSING`.
///   + `FAILED`: Đánh dấu outbox `FAILED` kèm mã lỗi.
///
/// Lưu ý kiến trúc:
/// - Access Session là token tạm thời (ephemeral capability), không tạo bảng resource vật lý lâu dài
///   trong PostgreSQL. Dòng outbox chính là Source of Truth duy nhất ở Central kiểm tra tính sẵn sàng.
pub async fn resolve_access_prepare(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_access_prepare",
        &format!(
            "Settling storage access preparation: {} -> {}",
            job_uuid, status
        ),
    );

    let row = if status == "SUCCEEDED" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                     error_code = NULL, error_message = NULL \
                 WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await?
    } else if status == "PROCESSING" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, updated_at = NOW(), error_code = NULL, error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'FAILED', completed_at = NOW(), updated_at = NOW(), error_code = $1, error_message = $2 \
                 WHERE event_id = $3::uuid AND job_topic = $4 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&error_code, &error_message, &job_uuid, &job_topic],
            )
            .await?
    };

    Ok(row)
}
