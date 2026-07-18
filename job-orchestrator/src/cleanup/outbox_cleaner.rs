use crate::observability::logger::Logger;
use std::time::Duration;
use tokio::time::sleep;
use tokio_postgres::NoTls;

/// [COMMENT]: Worker chạy định kỳ xóa các bản ghi outbox đã ở trạng thái terminal
/// (SUCCEEDED hoặc FAILED) hơn 30 ngày.
/// Sử dụng index `idx_storage_outbox_terminal_cleanup` và xóa theo batch (LIMIT 500)
/// để tránh lock lớn và gián đoạn IOPS của DB SoT.
pub async fn run_outbox_cleanup_loop(db_url: String) {
    Logger::sys_info(
        "outbox_cleaner",
        "Khởi động 30-day Data Retention Cleanup Worker Loop...",
    );

    loop {
        if let Err(e) = cleanup_terminal_outbox_batch(&db_url).await {
            Logger::sys_error(
                "outbox_cleaner",
                "Lỗi trong vòng lặp dọn dẹp outbox records cũ",
                &e.to_string(),
            );
        }

        // [COMMENT]: Chạy dọn dẹp mỗi 6 giờ một lần
        sleep(Duration::from_secs(6 * 3600)).await;
    }
}

async fn cleanup_terminal_outbox_batch(
    db_url: &str,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error("outbox_cleaner.db", "Lỗi DB connection", &e.to_string());
        }
    });

    let retention_days: i32 = 30;
    let batch_limit: i64 = 500;

    // [COMMENT]: Xóa theo batch nhỏ 500 bản ghi bằng CTE dựa trên partial index idx_storage_outbox_terminal_cleanup
    let deleted_count = pg_client
        .execute(
            "WITH to_delete AS ( \
                 SELECT id FROM storage.storage_outbox_records \
                 WHERE status IN ('SUCCEEDED', 'FAILED') \
                   AND completed_at IS NOT NULL \
                   AND completed_at < NOW() - ($1 || ' days')::INTERVAL \
                 ORDER BY completed_at ASC, id ASC \
                 LIMIT $2 \
             ) \
             DELETE FROM storage.storage_outbox_records \
             WHERE id IN (SELECT id FROM to_delete)",
            &[&retention_days, &batch_limit],
        )
        .await?;

    if deleted_count > 0 {
        Logger::sys_info(
            "outbox_cleaner",
            &format!(
                "Đã dọn dẹp thành công {} bản ghi terminal outbox quá 30 ngày",
                deleted_count
            ),
        );
    }

    Ok(())
}
