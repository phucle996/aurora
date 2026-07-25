use crate::config::Config;
use crate::contracts::mail::MailConsumerRuntimeReportBatchV1;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use prost::Message;
use uuid::Uuid;

const REPORT_SUBJECT: &str = "aurora.runtime.reports.*.mail.consumer.v1";
const REPORT_QUEUE: &str = "job-orchestrator-mail-runtime-report-v1";
const REPORT_STREAM: &str = "mail:consumer:reports";

/// [COMMENT]: NATS Core report là at-most-once soft state. JO ghi ngay vào Shared Redis Stream
/// để các replica aggregate/fence bằng consumer group; lỗi Redis chỉ mất sample hiện tại.
pub async fn run_runtime_report_nats_bridge(
    config: &Config,
    redis_client: &redis::Client,
    nats_client: &async_nats::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut subscription = nats_client
        .queue_subscribe(REPORT_SUBJECT.to_string(), REPORT_QUEUE.to_string())
        .await?;
    let mut redis_conn =
        crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
    Logger::sys_info(
        "mail.runtime_report_bridge",
        &format!("Listening on NATS Core subject {REPORT_SUBJECT}"),
    );

    while let Some(message) = subscription.next().await {
        if message.payload.is_empty() || message.payload.len() > 512 << 10 {
            continue;
        }
        let Ok(batch) = MailConsumerRuntimeReportBatchV1::decode(message.payload.as_ref()) else {
            continue;
        };
        let Ok(zone_id) = Uuid::from_slice(&batch.zone_id) else {
            continue;
        };
        let subject_zone = message
            .subject
            .as_str()
            .strip_prefix("aurora.runtime.reports.")
            .and_then(|suffix| suffix.strip_suffix(".mail.consumer.v1"))
            .and_then(|value| Uuid::parse_str(value).ok());
        if subject_zone != Some(zone_id) || batch.reports.is_empty() || batch.reports.len() > 250 {
            continue;
        }

        let result: redis::RedisResult<String> = redis::cmd("XADD")
            .arg(REPORT_STREAM)
            .arg("MAXLEN")
            .arg("~")
            .arg(100_000)
            .arg("*")
            .arg("zone_id")
            .arg(zone_id.to_string())
            .arg("payload")
            .arg(message.payload.as_ref())
            .query_async(&mut redis_conn)
            .await;
        if let Err(error) = result {
            Logger::sys_warn(
                "mail.runtime_report_bridge.redis",
                "Shared Redis rejected runtime sample; next NATS heartbeat will recover",
                &error.to_string(),
            );
            redis_conn =
                crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
        }
    }
    Err("NATS Core runtime report subscription ended".into())
}
