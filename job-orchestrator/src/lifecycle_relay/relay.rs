use crate::observability::logger::Logger;
use async_nats::jetstream;

use std::time::Duration;
use tokio::time::sleep;
use tokio_postgres::NoTls;
use uuid::Uuid;

/// [COMMENT]: Worker chạy ngầm quét bảng `storage.resource_lifecycle_events`
/// lấy các sự kiện `UNPUBLISHED` để publish lên NATS JetStream.
/// Sau khi nhận `PubAck` từ JetStream, worker cập nhật status = `PUBLISHED` và `published_at = NOW()`.
pub async fn run_relay_loop(db_url: String, nats_url: String) {
    Logger::sys_info(
        "lifecycle_relay",
        "Khởi động Resource Lifecycle Relay Background Loop...",
    );

    let replica_id = format!(
        "job-orchestrator-{}",
        hostname::get().unwrap_or_default().to_string_lossy()
    );

    loop {
        if let Err(e) = process_unpublished_batch(&db_url, &nats_url, &replica_id).await {
            Logger::sys_error(
                "lifecycle_relay",
                "Lỗi trong vòng lặp publish lifecycle event sang JetStream",
                &e.to_string(),
            );
            sleep(Duration::from_secs(5)).await;
        } else {
            // [COMMENT]: Thời gian nghỉ giữa các lần quét batch
            sleep(Duration::from_secs(2)).await;
        }
    }
}

async fn process_unpublished_batch(
    db_url: &str,
    nats_url: &str,
    replica_id: &str,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // 1. Kết nối PostgreSQL
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error("lifecycle_relay.db", "Lỗi DB connection", &e.to_string());
        }
    });

    // 2. Kết nối NATS & khởi tạo JetStream Context
    let nats_client = async_nats::connect(nats_url).await?;
    let js = jetstream::new(nats_client);

    // [COMMENT]: Đảm bảo Stream CONTROLPLANE_DOMAIN_EVENTS tồn tại trên NATS Server
    let stream_name = "CONTROLPLANE_DOMAIN_EVENTS";
    let subject = "billing.ownership.resource.changed.v1";

    let _stream = js
        .get_or_create_stream(jetstream::stream::Config {
            name: stream_name.to_string(),
            subjects: vec![subject.to_string()],
            retention: jetstream::stream::RetentionPolicy::Limits,
            max_age: Duration::from_secs(72 * 3600), // 72 hours
            ..Default::default()
        })
        .await?;

    // 3. Claim lease batch UNPUBLISHED records với FOR UPDATE SKIP LOCKED
    let rows = pg_client
        .query(
            "UPDATE storage.resource_lifecycle_events \
             SET locked_by = $1, locked_until = NOW() + INTERVAL '30 seconds', attempt_count = attempt_count + 1 \
             WHERE id IN ( \
                 SELECT id FROM storage.resource_lifecycle_events \
                 WHERE status = 'UNPUBLISHED' AND (locked_until IS NULL OR locked_until < NOW()) \
                 ORDER BY occurred_at ASC \
                 LIMIT 50 \
                 FOR UPDATE SKIP LOCKED \
             ) \
             RETURNING id, event_id, payload, traceparent",
            &[&replica_id],
        )
        .await?;

    if rows.is_empty() {
        return Ok(());
    }

    Logger::sys_info(
        "lifecycle_relay",
        &format!(
            "Đã claim lease {} lifecycle events để publish sang JetStream",
            rows.len()
        ),
    );

    for row in rows {
        let row_id: Uuid = row.get(0);
        let event_id: Uuid = row.get(1);
        let payload: Vec<u8> = row.get(2);
        let traceparent: Option<String> = row.get(3);

        // Build headers cho JetStream deduplication & metadata
        let mut headers = async_nats::HeaderMap::new();
        // [COMMENT]: Nats-Msg-Id giúp JetStream tự động de-duplicate tin nhắn nếu replay hoặc retry publish
        headers.insert("Nats-Msg-Id", event_id.to_string().as_str());
        headers.insert("Content-Type", "application/protobuf");
        headers.insert("Schema-Version", "1");
        if let Some(tp) = traceparent {
            if !tp.is_empty() {
                headers.insert("traceparent", tp.as_str());
            }
        }

        // 4. Publish message sang JetStream và đợi PubAck
        let ack_future = js
            .publish_with_headers(subject.to_string(), headers, payload.into())
            .await;

        match ack_future {
            Ok(ack) => {
                let ack_res = ack.await;
                if let Ok(_pub_ack) = ack_res {
                    // 5. Publish thành công -> Đánh dấu status = PUBLISHED trong DB
                    pg_client
                        .execute(
                            "UPDATE storage.resource_lifecycle_events \
                             SET status = 'PUBLISHED', published_at = NOW(), locked_by = NULL, locked_until = NULL \
                             WHERE id = $1",
                            &[&row_id],
                        )
                        .await?;
                } else {
                    let err_msg = format!("Lỗi PubAck từ JetStream: {:?}", ack_res.err());
                    mark_event_failed(&pg_client, row_id, &err_msg).await?;
                }
            }
            Err(e) => {
                let err_msg = format!("Lỗi publish sang NATS JetStream: {}", e);
                mark_event_failed(&pg_client, row_id, &err_msg).await?;
            }
        }
    }

    Ok(())
}

async fn mark_event_failed(
    pg_client: &tokio_postgres::Client,
    row_id: Uuid,
    error_msg: &str,
) -> Result<(), tokio_postgres::Error> {
    pg_client
        .execute(
            "UPDATE storage.resource_lifecycle_events \
             SET last_error = $1, locked_by = NULL, locked_until = NULL, \
                 status = CASE WHEN attempt_count >= 10 THEN 'DEAD' ELSE 'UNPUBLISHED' END \
             WHERE id = $2",
            &[&error_msg, &row_id],
        )
        .await?;
    Ok(())
}
