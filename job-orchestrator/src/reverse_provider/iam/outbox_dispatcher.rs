use crate::observability::logger::Logger;
use redis::aio::MultiplexedConnection;
use std::time::Duration;
use tokio_postgres::{Client, NoTls, Row};

const DISPATCH_BATCH: i64 = 50;
const REDIS_DEDUPE_TTL_SECONDS: i64 = 30 * 24 * 60 * 60;
const IDLE_RECONCILE_SECONDS: u64 = 30;

const XADD_ONCE_SCRIPT: &str = r#"
local existing = redis.call('GET', KEYS[1])
if existing then
  return existing
end
local entry_id = redis.call('XADD', KEYS[2], '*',
  'job_id', ARGV[1],
  'job_version', ARGV[2],
  'attempt', ARGV[3],
  'job_topic', ARGV[4],
  'source_domain', 'IAM',
  'resource_id', ARGV[5],
  'payload_schema_version', ARGV[6],
  'payload', ARGV[7],
  'trace_id', ARGV[8],
  'idle', ARGV[9])
redis.call('SET', KEYS[1], entry_id, 'EX', ARGV[10])
return entry_id
"#;

// [COMMENT]: Dispatcher drain nhanh khi có backlog; khi rỗng chỉ reconciliation chậm để không poll DB mỗi 500ms.
pub async fn run_iam_outbox_dispatch_loop(database_url: String, redis_client: redis::Client) {
    Logger::sys_info(
        "iam_outbox_dispatcher.start",
        "Khởi động IAM outbox lease dispatcher và periodic reconciliation",
    );

    loop {
        match tokio_postgres::connect(&database_url, NoTls).await {
            Ok((client, connection)) => {
                tokio::spawn(async move {
                    if let Err(error) = connection.await {
                        Logger::sys_error(
                            "iam_outbox_dispatcher.db_connection",
                            "IAM outbox DB connection stopped",
                            &error.to_string(),
                        );
                    }
                });
                if let Err(error) = dispatch_until_connection_fails(&client, &redis_client).await {
                    Logger::sys_error(
                        "iam_outbox_dispatcher.run",
                        "IAM outbox dispatcher iteration failed; reconnecting",
                        &error.to_string(),
                    );
                }
            }
            Err(error) => Logger::sys_error(
                "iam_outbox_dispatcher.connect",
                "Cannot connect PostgreSQL for IAM outbox reconciliation",
                &error.to_string(),
            ),
        }
        tokio::time::sleep(Duration::from_secs(2)).await;
    }
}

async fn dispatch_until_connection_fails(
    client: &Client,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let mut redis = redis_client.get_multiplexed_tokio_connection().await?;
    loop {
        let rows = claim_batch(client).await?;
        let had_rows = !rows.is_empty();
        for row in rows {
            dispatch_row(client, &mut redis, row).await?;
        }
        if had_rows {
            // [COMMENT]: Yield ngắn giữa các batch để drain backlog mà không tạo busy loop chiếm executor.
            tokio::time::sleep(Duration::from_millis(25)).await;
        } else {
            // [COMMENT]: Jitter theo clock tránh các JO replica cùng claim DB ở một nhịp cố định.
            let jitter = (std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .subsec_nanos() as u64)
                % 10;
            tokio::time::sleep(Duration::from_secs(IDLE_RECONCILE_SECONDS + jitter)).await;
        }
    }
}

async fn claim_batch(client: &Client) -> Result<Vec<Row>, tokio_postgres::Error> {
    client
        .query(
            "WITH exhausted AS ( \
               UPDATE iam.iam_outbox_records \
               SET status='FAILED', completed_at=NOW(), error_code='DISPATCH_RETRY_EXHAUSTED', \
                   error_message=COALESCE(last_dispatch_error, 'dispatch retry exhausted'), lease_until=NULL \
               WHERE status IN ('PENDING', 'PUBLISHING') AND attempts >= 25 \
               RETURNING id \
             ), candidates AS ( \
               SELECT id FROM iam.iam_outbox_records \
               WHERE attempts < 25 AND ( \
                 (status='PENDING' AND available_at <= NOW()) OR \
                 (status='PUBLISHING' AND lease_until < NOW()) \
               ) \
               ORDER BY available_at, id FOR UPDATE SKIP LOCKED LIMIT $1 \
             ), claimed AS ( \
               UPDATE iam.iam_outbox_records o \
               SET status='PUBLISHING', lease_until=NOW() + INTERVAL '30 seconds', attempts=attempts+1 \
               FROM candidates c WHERE o.id=c.id \
               RETURNING o.id, o.event_id, o.routing_scope, o.job_topic, o.payload, o.job_version, \
                         o.resource_id, o.payload_schema_version, o.trace_id, o.idle \
             ) SELECT * FROM claimed",
            &[&DISPATCH_BATCH],
        )
        .await
}

async fn dispatch_row(
    client: &Client,
    redis: &mut MultiplexedConnection,
    row: Row,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let id: i64 = row.get(0);
    let event_id: uuid::Uuid = row.get(1);
    let routing_scope: String = row.get(2);
    let job_topic: String = row.get(3);
    let payload: Vec<u8> = row.get(4);
    let job_version: i32 = row.get(5);
    let resource_id: Option<String> = row.get(6);
    let payload_schema_version: i32 = row.get(7);
    let trace_id: Option<Vec<u8>> = row.get(8);
    let idle: Option<i32> = row.get(9);

    let stream = if routing_scope == "platform" || routing_scope == "global" {
        "jobs:platform".to_string()
    } else if let Some(zone_id) = routing_scope.strip_prefix("zone:") {
        format!("jobs:{zone_id}")
    } else {
        format!("jobs:{routing_scope}")
    };
    let dedupe_key = format!("job_dispatch:iam:{event_id}");

    // [COMMENT]: Lua gắn marker và XADD nguyên tử trong Redis; crash sau XADD trước DB update không tạo stream entry thứ hai.
    let publish: Result<String, redis::RedisError> = redis::cmd("EVAL")
        .arg(XADD_ONCE_SCRIPT)
        .arg(2)
        .arg(&dedupe_key)
        .arg(&stream)
        .arg(event_id.to_string())
        .arg(job_version.to_string())
        .arg("0")
        .arg(&job_topic)
        .arg(resource_id.unwrap_or_default())
        .arg(payload_schema_version.to_string())
        .arg(payload)
        .arg(trace_id.unwrap_or_default())
        .arg(idle.map(|value| value.to_string()).unwrap_or_default())
        .arg(REDIS_DEDUPE_TTL_SECONDS)
        .query_async(redis)
        .await;

    match publish {
        Ok(_) => {
            client
                .execute(
                    "UPDATE iam.iam_outbox_records SET status='PUBLISHED', lease_until=NULL, \
                     last_dispatch_error=NULL WHERE id=$1 AND status='PUBLISHING'",
                    &[&id],
                )
                .await?;
            Ok(())
        }
        Err(error) => {
            client
                .execute(
					"UPDATE iam.iam_outbox_records SET status='PENDING', lease_until=NULL, last_dispatch_error=LEFT($2, 2000), \
                     available_at=NOW() + (LEAST(300, POWER(2, LEAST(attempts, 8))) * INTERVAL '1 second') \
                     WHERE id=$1 AND status='PUBLISHING'",
                    &[&id, &error.to_string()],
                )
                .await?;
            Err(Box::new(error))
        }
    }
}
