use super::super::MailRuntime;
use crate::config::Config;
use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeReportedV1, MailConsumerRuntimeState,
    MailEventMetadataV1,
};
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use prost::Message;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use uuid::Uuid;

/// [COMMENT]: Mỗi pod đọc snapshot trực tiếp từ app memory của chính nó. Reporter không ghi/scan
/// Zone NATS KV; Central Redis chỉ nhận consumer đang có watch lease ngắn do UI mở Detail.
pub(super) fn start(
    config: Arc<Config>,
    runtime_redis: Arc<RedisClientManager>,
    runtime: Arc<MailRuntime>,
) {
    tokio::spawn(async move {
        let event_namespace = Uuid::parse_str("e7b1a4a4-9150-4494-88d4-5994d312d219")
            .expect("mail consumer report namespace must be valid");
        let mut redis_connection: Option<redis::aio::MultiplexedConnection> = None;

        loop {
            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let snapshots = runtime.runtime_snapshots();

            if redis_connection.is_none() {
                redis_connection = runtime_redis
                    .client()
                    .get_multiplexed_tokio_connection()
                    .await
                    .ok();
            }

            let mut transport_failed = false;
            if !snapshots.is_empty() {
                if let Some(connection) = redis_connection.as_mut() {
                    // [COMMENT]: Zone watch index làm cost tỉ lệ với số Detail đang mở, không
                    // với toàn bộ consumer/slot đang chạy trong pod.
                    let watched_consumer_ids: redis::RedisResult<Vec<String>> = redis::Script::new(
                        "local t=redis.call('TIME'); \
                             local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000); \
                             redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now); \
                             return redis.call('ZRANGEBYSCORE',KEYS[1],now,'+inf')",
                    )
                    .key(format!("mail:runtime:watch-index:{}", config.zone_id))
                    .invoke_async(connection)
                    .await;
                    let watched_consumer_ids = match watched_consumer_ids {
                        Ok(values) => values,
                        Err(_) => {
                            transport_failed = true;
                            Vec::new()
                        }
                    };
                    let watch_keys = watched_consumer_ids
                        .iter()
                        .map(|consumer_id| {
                            format!(
                                "mail:runtime:watch-active:{}:{}",
                                config.zone_id, consumer_id
                            )
                        })
                        .collect::<Vec<_>>();
                    let leases: redis::RedisResult<Vec<Option<String>>> = if watch_keys.is_empty() {
                        Ok(Vec::new())
                    } else {
                        redis::cmd("MGET")
                            .arg(&watch_keys)
                            .query_async(connection)
                            .await
                    };

                    match leases {
                        Ok(leases) => {
                            let active_leases = watched_consumer_ids
                                .into_iter()
                                .zip(leases)
                                .filter_map(|(consumer_id, lease)| {
                                    lease.map(|lease| (consumer_id, lease))
                                })
                                .collect::<HashMap<_, _>>();
                            let mut candidates =
                                Vec::<(String, String, MailConsumerRuntimeReportedV1)>::new();

                            for snapshot in snapshots {
                                let Some(active_lease) = active_leases.get(&snapshot.consumer_id)
                                else {
                                    continue;
                                };
                                // [COMMENT]: Config version nằm trong lease token để runtime cũ
                                // không tiếp tục cấp dữ liệu sau update/publish COW.
                                if !active_lease
                                    .starts_with(&format!("{}:", snapshot.config_version))
                                    || snapshot.report_sequence == 0
                                    || snapshot.runtime_generation == 0
                                    || snapshot.runtime_generation != snapshot.fencing_token
                                    || snapshot.config_version == 0
                                    || snapshot.heartbeat_unix_ms > i64::MAX as u64
                                    || snapshot.instance_id != format!("slot:{}", snapshot.slot)
                                    || snapshot.runtime_node_id.is_empty()
                                    || snapshot.runtime_node_id.len() > 255
                                    || snapshot.runtime_node_id.chars().any(char::is_control)
                                    || now_ms.saturating_sub(snapshot.heartbeat_unix_ms)
                                        > config
                                            .mail_stream_slot_lease_ttl_seconds
                                            .saturating_mul(2_000)
                                {
                                    continue;
                                }
                                let Some((_, runtime_epoch)) = active_lease.split_once(':') else {
                                    continue;
                                };
                                if Uuid::parse_str(runtime_epoch).is_err() {
                                    continue;
                                }

                                let consumer_id = match Uuid::parse_str(&snapshot.consumer_id) {
                                    Ok(value) => value,
                                    Err(_) => continue,
                                };
                                let runtime_boot_id =
                                    match Uuid::parse_str(&snapshot.runtime_boot_id) {
                                        Ok(value) => value,
                                        Err(_) => continue,
                                    };
                                let runtime_state = match snapshot.state.as_str() {
                                    "STOPPED" => MailConsumerRuntimeState::Stopped,
                                    "STARTING" => MailConsumerRuntimeState::Starting,
                                    "RUNNING" => MailConsumerRuntimeState::Running,
                                    "PAUSED" => MailConsumerRuntimeState::Paused,
                                    "DRAINING" => MailConsumerRuntimeState::Draining,
                                    "ERROR" => MailConsumerRuntimeState::Error,
                                    "DEGRADED" => MailConsumerRuntimeState::Degraded,
                                    _ => continue,
                                };
                                let error_code = if snapshot.error_code.len() <= 100
                                    && snapshot.error_code.bytes().all(|byte| {
                                        byte.is_ascii_uppercase()
                                            || byte.is_ascii_digit()
                                            || byte == b'_'
                                    }) {
                                    snapshot.error_code
                                } else {
                                    "MAIL_RUNTIME_ERROR_INVALID".to_string()
                                };
                                let event_name = format!(
                                    "{}:{}:{}:{}:{}:{}",
                                    config.zone_id,
                                    consumer_id,
                                    snapshot.instance_id,
                                    snapshot.runtime_generation,
                                    snapshot.report_sequence,
                                    runtime_epoch
                                );
                                let event_id =
                                    Uuid::new_v5(&event_namespace, event_name.as_bytes());
                                let gate_key = format!(
                                    "mail:consumer:report:gate:{}:{}:{}",
                                    config.zone_id, consumer_id, snapshot.instance_id
                                );
                                // [COMMENT]: Epoch trong signature làm lần watch mới bypass gate
                                // ngay; lag/state/takeover cũng phát delta tức thì.
                                let gate_signature = format!(
                                    "{}:{}:{}:{}:{}:{}:{}:{}",
                                    active_lease,
                                    snapshot.config_version,
                                    snapshot.runtime_generation,
                                    runtime_state as i32,
                                    snapshot.runtime_node_id,
                                    runtime_boot_id,
                                    error_code,
                                    snapshot.consumer_lag
                                );
                                candidates.push((
                                    gate_key,
                                    gate_signature,
                                    MailConsumerRuntimeReportedV1 {
                                        metadata: Some(MailEventMetadataV1 {
                                            event_id: event_id.as_bytes().to_vec(),
                                            schema_version: 1,
                                            occurred_at_unix_ms: snapshot.heartbeat_unix_ms as i64,
                                            traceparent: String::new(),
                                            producer: "dataplane-mail-consumer".to_string(),
                                        }),
                                        consumer_id: consumer_id.as_bytes().to_vec(),
                                        config_version: snapshot.config_version,
                                        runtime_state: runtime_state as i32,
                                        instance_id: snapshot.instance_id,
                                        runtime_generation: snapshot.runtime_generation,
                                        consumer_lag: snapshot.consumer_lag,
                                        error_code,
                                        // [COMMENT]: Raw broker exception không vượt reverse trust boundary.
                                        error_message: String::new(),
                                        report_sequence: snapshot.report_sequence,
                                        runtime_epoch: runtime_epoch.to_string(),
                                    },
                                ));
                            }

                            if !candidates.is_empty() {
                                let gate_keys = candidates
                                    .iter()
                                    .map(|(key, _, _)| key.as_str())
                                    .collect::<Vec<_>>();
                                let previous: redis::RedisResult<Vec<Option<String>>> =
                                    redis::cmd("MGET")
                                        .arg(&gate_keys)
                                        .query_async(connection)
                                        .await;
                                match previous {
                                    Ok(previous) => {
                                        let selected = candidates
                                            .into_iter()
                                            .zip(previous)
                                            .filter_map(|(candidate, old)| {
                                                (old.as_deref() != Some(candidate.1.as_str()))
                                                    .then_some(candidate)
                                            })
                                            .collect::<Vec<_>>();

                                        for chunk in selected.chunks(250) {
                                            let batch = MailConsumerRuntimeReportBatchV1 {
                                                reports: chunk
                                                    .iter()
                                                    .map(|(_, _, report)| report.clone())
                                                    .collect(),
                                            };
                                            let mut payload =
                                                Vec::with_capacity(batch.encoded_len());
                                            if batch.encode(&mut payload).is_err()
                                                || payload.len() > 512 << 10
                                            {
                                                Logger::sys_warn(
                                                    "mail.supervisor.consumer_reporter",
                                                    "Consumer report batch exceeded its bounded contract",
                                                    "MAIL_CONSUMER_REPORT_BATCH_INVALID",
                                                );
                                                continue;
                                            }

                                            // [COMMENT]: XADD và gate commit cùng Lua invocation.
                                            // Gate TTL 10s vừa hạn chế write vừa refresh snapshot
                                            // trước khi watch lease 30s hết hạn.
                                            let script = redis::Script::new(
                                                "local t=redis.call('TIME'); \
                                                 local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000); \
                                                 local min_id=string.format('%.0f-0',now-tonumber(ARGV[3])); \
                                                 local id=redis.call('XADD',KEYS[1],'MINID','~',min_id,'*','zone_id',ARGV[1],'payload',ARGV[2]); \
                                                 for i=2,#KEYS do redis.call('SET',KEYS[i],ARGV[i+2],'PX',10000) end; \
                                                 return id",
                                            );
                                            let mut invocation = script.prepare_invoke();
                                            invocation
                                                .key("mail:consumer:reports")
                                                .arg(&config.zone_id)
                                                .arg(payload)
                                                .arg(3_600_000_u64);
                                            for (gate_key, _, _) in chunk {
                                                invocation.key(gate_key);
                                            }
                                            for (_, gate_signature, _) in chunk {
                                                invocation.arg(gate_signature);
                                            }
                                            let published: redis::RedisResult<String> =
                                                invocation.invoke_async(connection).await;
                                            if published.is_err() {
                                                transport_failed = true;
                                                break;
                                            }
                                        }
                                    }
                                    Err(_) => transport_failed = true,
                                }
                            }
                        }
                        Err(_) => transport_failed = true,
                    }
                }
            }
            if transport_failed {
                redis_connection = None;
            }

            tokio::time::sleep(Duration::from_millis(
                config.mail_consumer_report_interval_ms + rand::random::<u64>() % 1_000,
            ))
            .await;
        }
    });
}
