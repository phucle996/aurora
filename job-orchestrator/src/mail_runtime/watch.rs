use crate::config::Config;
use crate::contracts::mail::MailConsumerRuntimeWatchRequestedV1;
use crate::observability::logger::Logger;
use chrono::Utc;
use prost::Message;
use uuid::Uuid;

const WATCH_STREAM: &str = "mail:runtime:watch-requests";
const WATCH_GROUP: &str = "job-orchestrator-mail-runtime-watch-v1";

/// [COMMENT]: CP chỉ ghi Shared L2 Redis Stream. JO là bridge duy nhất có cả credential
/// Shared Redis và NATS Core; ACK/XDEL chỉ sau khi NATS server chấp nhận publish.
pub async fn run_runtime_watch_bridge(
    config: &Config,
    redis_client: &redis::Client,
    nats_client: &async_nats::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut redis_conn =
        crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(WATCH_STREAM)
        .arg(WATCH_GROUP)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    let listener_id = format!(
        "mail-runtime-watch-{}-{}",
        crate::config::get_node_hostname(),
        std::process::id()
    );

    let mut claim_cursor = "0-0".to_string();
    loop {
        let claimed: redis::Value = redis::cmd("XAUTOCLAIM")
            .arg(WATCH_STREAM)
            .arg(WATCH_GROUP)
            .arg(&listener_id)
            .arg(config.workflows.mail.runtime_report_claim_idle_ms)
            .arg(&claim_cursor)
            .arg("COUNT")
            .arg(100)
            .query_async(&mut redis_conn)
            .await?;
        let reply = if let redis::Value::Bulk(parts) = claimed {
            if let Some(redis::Value::Data(next)) = parts.first() {
                claim_cursor = String::from_utf8_lossy(next).into_owned();
            }
            if let Some(redis::Value::Bulk(entries)) = parts.get(1) {
                if entries.is_empty() {
                    redis::cmd("XREADGROUP")
                        .arg("GROUP")
                        .arg(WATCH_GROUP)
                        .arg(&listener_id)
                        .arg("BLOCK")
                        .arg(2_000)
                        .arg("COUNT")
                        .arg(100)
                        .arg("STREAMS")
                        .arg(WATCH_STREAM)
                        .arg(">")
                        .query_async(&mut redis_conn)
                        .await?
                } else {
                    redis::Value::Bulk(vec![redis::Value::Bulk(vec![
                        redis::Value::Data(WATCH_STREAM.as_bytes().to_vec()),
                        redis::Value::Bulk(entries.clone()),
                    ])])
                }
            } else {
                redis::Value::Nil
            }
        } else {
            redis::Value::Nil
        };

        let redis::Value::Bulk(streams) = reply else {
            continue;
        };
        let Some(redis::Value::Bulk(stream_data)) = streams.first() else {
            continue;
        };
        let Some(redis::Value::Bulk(entries)) = stream_data.get(1) else {
            continue;
        };

        for entry in entries {
            let redis::Value::Bulk(parts) = entry else {
                continue;
            };
            let Some(redis::Value::Data(entry_id)) = parts.first() else {
                continue;
            };
            let entry_id = String::from_utf8_lossy(entry_id).into_owned();
            let fields = match parts.get(1) {
                Some(redis::Value::Bulk(fields)) => fields.as_slice(),
                _ => &[],
            };
            let mut payload = None;
            for field in fields.chunks(2) {
                if field.len() == 2
                    && matches!(&field[0], redis::Value::Data(name) if name == b"payload")
                {
                    if let redis::Value::Data(value) = &field[1] {
                        payload = Some(value.as_slice());
                    }
                }
            }

            let now_ms = Utc::now().timestamp_millis();
            let mut watch = payload
                .filter(|payload| payload.len() <= 64 << 10)
                .and_then(|payload| MailConsumerRuntimeWatchRequestedV1::decode(payload).ok());
            let valid = watch.as_ref().is_some_and(|watch| {
                let metadata = watch.metadata.as_ref();
                Uuid::from_slice(&watch.zone_id).is_ok()
                    && Uuid::from_slice(&watch.consumer_id).is_ok()
                    && watch.config_version > 0
                    && Uuid::parse_str(&watch.runtime_epoch).is_ok()
                    && watch.expires_at_unix_ms > now_ms
                    && watch.expires_at_unix_ms <= now_ms.saturating_add(60_000)
                    && metadata.is_some_and(|metadata| {
                        Uuid::from_slice(&metadata.event_id).is_ok()
                            && metadata.schema_version == 1
                            && metadata.producer == "controlplane-mail-runtime-watch"
                            && metadata.traceparent.len() <= 128
                            && metadata.occurred_at_unix_ms <= now_ms.saturating_add(300_000)
                            && metadata.occurred_at_unix_ms >= now_ms.saturating_sub(300_000)
                    })
            });

            let terminal = if valid {
                let watch = watch.as_mut().expect("validated runtime watch");
                let zone_id = Uuid::from_slice(&watch.zone_id).expect("validated zone UUID");
                if let Some(metadata) = watch.metadata.as_mut() {
                    // [COMMENT]: Producer ghi đúng hop cuối để Dataplane không phải tin payload do CP
                    // có thể bị publish nhầm thẳng lên NATS subject.
                    metadata.producer = "job-orchestrator-mail-runtime-watch".to_string();
                }
                let subject = format!("aurora.runtime.watch.{zone_id}.mail.consumer.v1");
                match nats_client
                    .publish(subject, watch.encode_to_vec().into())
                    .await
                {
                    Ok(()) => match nats_client.flush().await {
                        // [COMMENT]: Core NATS has no per-message ACK; PING/PONG flush is the server-acceptance
                        // boundary for removing this soft-state request from the Redis Stream.
                        Ok(()) => true,
                        Err(error) => {
                            Logger::sys_warn(
                                "mail.runtime_watch_bridge.flush",
                                "NATS Core flush failed; Redis Stream entry remains pending",
                                &error.to_string(),
                            );
                            false
                        }
                    },
                    Err(error) => {
                        Logger::sys_warn(
                            "mail.runtime_watch_bridge.publish",
                            "NATS Core publish failed; Redis Stream entry remains pending",
                            &error.to_string(),
                        );
                        false
                    }
                }
            } else {
                // [COMMENT]: Poison/expired watch là terminal soft state; retry không thể làm nó hợp lệ.
                true
            };

            if terminal {
                let _: redis::RedisResult<i64> = redis::Script::new(
                    "local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); \
                     if acked == 1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; \
                     return 0",
                )
                .key(WATCH_STREAM)
                .arg(WATCH_GROUP)
                .arg(&entry_id)
                .invoke_async(&mut redis_conn)
                .await;
            }
        }
    }
}
