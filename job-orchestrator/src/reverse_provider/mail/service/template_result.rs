/// [COMMENT]: Template business data/tombstone đã commit tại Controlplane. Result service chỉ
/// đóng trạng thái outbox; FAILED không được xóa immutable version hoặc durable delete tombstone.
pub async fn apply_result(
    pg_client: &mut tokio_postgres::Client,
    event_id: uuid::Uuid,
    job_topic: &str,
    status: &str,
    attempt: u32,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> {
    let transaction = pg_client.transaction().await?;
    let locked = transaction
        .query_opt(
            "SELECT status,result_attempt \
             FROM mail.mail_outbox_records \
             WHERE event_id=$1 AND job_topic=$2 \
             FOR UPDATE",
            &[&event_id, &job_topic],
        )
        .await?;
    let Some(locked) = locked else {
        transaction.commit().await?;
        return Ok(None);
    };
    let current_status: String = locked.get(0);
    let current_attempt: i32 = locked.get(1);
    let attempt = attempt as i32;
    // [COMMENT]: Template result dùng attempt fence giống consumer nhưng giữ riêng câu lệnh
    // để lifecycle immutable template không bị ẩn sau helper dùng chung.
    let transition_allowed = current_status != "SUCCEEDED"
        && match status {
            "SUCCEEDED" => true,
            "PROCESSING" => current_status == "PENDING" || attempt > current_attempt,
            "FAILED" => {
                attempt > current_attempt
                    || (attempt == current_attempt && current_status != "FAILED")
            }
            _ => false,
        };
    if !transition_allowed {
        transaction.commit().await?;
        return Ok(None);
    }

    // [COMMENT]: Reconciler có thể heal một event từng FAILED, vì vậy SUCCEEDED được phép thắng
    // FAILED. PROCESSING không được hạ một terminal success trở lại trạng thái đang chạy.
    let row = transaction
        .query_opt(
            "UPDATE mail.mail_outbox_records \
             SET status=$1, completed_at=CASE WHEN $1='PROCESSING' THEN NULL ELSE NOW() END, \
                 updated_at=NOW(), \
                 error_code=CASE WHEN $1='FAILED' THEN $2 ELSE NULL END, \
                 error_message=CASE WHEN $1='FAILED' THEN $3 ELSE NULL END, \
                 result_attempt=GREATEST(result_attempt,$4) \
             WHERE event_id=$5 AND job_topic=$6 AND status<>'SUCCEEDED' \
             RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
            &[
                &status,
                &error_code,
                &error_message,
                &attempt,
                &event_id,
                &job_topic,
            ],
        )
        .await?;
    transaction.commit().await?;
    Ok(row)
}
