use crate::config::Config;
use crate::executor::mail::runtime::RuntimeHealthSnapshot;
use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeReportedV1, MailConsumerRuntimeState,
    MailEventMetadataV1,
};
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use prost::Message;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use uuid::Uuid;

/// [COMMENT]: Một rotating relay đọc logical slot heartbeat từ Zone KV rồi gửi bounded delta batch.
/// Redis gate được commit cùng XADD nên leader rotation không biến heartbeat ổn định thành write storm.
pub(super) fn start(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
) {
    tokio::spawn(async move {
        let reporter_id = std::env::var("HOSTNAME")
            .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
        let event_namespace = Uuid::parse_str("e7b1a4a4-9150-4494-88d4-5994d312d219")
            .expect("mail consumer report namespace must be valid");
        let mut redis_connection: Option<redis::aio::MultiplexedConnection> = None;

        loop {
            let lease = match zone_kv
                .acquire_rotating_lease(
                    "lease.mail.consumer.report",
                    &reporter_id,
                    Duration::from_secs(15),
                    Duration::from_secs(6),
                )
                .await
            {
                Ok(Some(lease)) => lease,
                Ok(None) => {
                    tokio::time::sleep(Duration::from_millis(
                        config.mail_consumer_report_interval_ms + rand::random::<u64>() % 1_000,
                    ))
                    .await;
                    continue;
                }
                Err(error) => {
                    Logger::sys_warn(
                        "mail.supervisor.consumer_reporter",
                        "Failed to acquire consumer report lease",
                        &error,
                    );
                    tokio::time::sleep(Duration::from_secs(2)).await;
                    continue;
                }
            };

            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let mut candidates = Vec::<(String, String, MailConsumerRuntimeReportedV1)>::new();

            for key in zone_kv.health_keys().await.unwrap_or_default() {
                if !key.starts_with("mail.runtime.") {
                    continue;
                }
                let Some(bytes) = zone_kv.health_get(key.clone()).await.ok().flatten() else {
                    continue;
                };
                let Ok(snapshot) = serde_json::from_slice::<RuntimeHealthSnapshot>(&bytes) else {
                    continue;
                };
                if snapshot.report_sequence == 0
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
                let consumer_id = match Uuid::parse_str(&snapshot.consumer_id) {
                    Ok(value) => value,
                    Err(_) => continue,
                };
                // [COMMENT]: Key và value phải cùng mô tả một logical slot; snapshot hỏng/nhân bản
                // trong KV không được khuếch đại thành nhiều Central Redis delta.
                if key != format!("mail.runtime.{}.{}", consumer_id, snapshot.slot) {
                    continue;
                }
                let runtime_boot_id = match Uuid::parse_str(&snapshot.runtime_boot_id) {
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
                        byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                    }) {
                    snapshot.error_code
                } else {
                    "MAIL_RUNTIME_ERROR_INVALID".to_string()
                };
                let event_name = format!(
                    "{}:{}:{}:{}:{}",
                    config.zone_id,
                    consumer_id,
                    snapshot.instance_id,
                    snapshot.runtime_generation,
                    snapshot.report_sequence
                );
                let event_id = Uuid::new_v5(&event_namespace, event_name.as_bytes());
                let gate_key = format!(
                    "mail:consumer:report:gate:{}:{}:{}",
                    config.zone_id, consumer_id, snapshot.instance_id
                );
                // [COMMENT]: Sequence không nằm trong signature; stable heartbeat refresh tối đa mỗi
                // gate TTL, còn state/config/generation/node takeover luôn bypass ngay lập tức.
                let gate_signature = format!(
                    "{}:{}:{}:{}:{}:{}",
                    snapshot.config_version,
                    snapshot.runtime_generation,
                    runtime_state as i32,
                    snapshot.runtime_node_id,
                    runtime_boot_id,
                    error_code
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
                        // [COMMENT]: Raw customer broker exception không vượt reverse trust boundary.
                        error_message: String::new(),
                        report_sequence: snapshot.report_sequence,
                    },
                ));
            }

            if redis_connection.is_none() {
                redis_connection = redis_job
                    .client()
                    .get_multiplexed_tokio_connection()
                    .await
                    .ok();
            }
            let mut transport_failed = false;
            if !candidates.is_empty() {
                if let Some(connection) = redis_connection.as_mut() {
                    let gate_keys = candidates
                        .iter()
                        .map(|(key, _, _)| key.as_str())
                        .collect::<Vec<_>>();
                    let previous: redis::RedisResult<Vec<Option<String>>> = redis::cmd("MGET")
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
                                if !zone_kv
                                    .renew_lease(&lease, Duration::from_secs(15))
                                    .await
                                    .unwrap_or(false)
                                {
                                    break;
                                }
                                let batch = MailConsumerRuntimeReportBatchV1 {
                                    reports: chunk
                                        .iter()
                                        .map(|(_, _, report)| report.clone())
                                        .collect(),
                                };
                                let mut payload = Vec::with_capacity(batch.encoded_len());
                                if batch.encode(&mut payload).is_err() || payload.len() > 512 << 10
                                {
                                    Logger::sys_warn(
                                        "mail.supervisor.consumer_reporter",
                                        "Consumer report batch exceeded its bounded contract",
                                        "MAIL_CONSUMER_REPORT_BATCH_INVALID",
                                    );
                                    continue;
                                }

                                // [COMMENT]: XADD xảy ra trước SET gate trong cùng Lua invocation;
                                // transport fail không thể làm mất delta vì gate chưa được commit.
                                let script = redis::Script::new(
                                    "local t=redis.call('TIME'); \
                                     local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000); \
                                     local min_id=string.format('%.0f-0',now-tonumber(ARGV[3])); \
                                     local id=redis.call('XADD',KEYS[1],'MINID','~',min_id,'*','zone_id',ARGV[1],'payload',ARGV[2]); \
                                     for i=2,#KEYS do redis.call('SET',KEYS[i],ARGV[i+2],'PX',60000) end; \
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
            if transport_failed {
                redis_connection = None;
            }

            let _ = zone_kv.release_lease(&lease).await;
            tokio::time::sleep(Duration::from_millis(
                config.mail_consumer_report_interval_ms + rand::random::<u64>() % 1_000,
            ))
            .await;
        }
    });
}
